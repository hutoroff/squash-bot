package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/hutoroff/squash-bot/internal/discovery"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrRegistryConflict = errors.New("service registry identity conflict")
	ErrRegistryNotFound = errors.New("service registry instance not found")
	ErrRegistryGone     = errors.New("service registry instance is expired or terminal")
	ErrRegistryCapacity = errors.New("service registry instance capacity exhausted")
)

const registryLeaseTTL = 35 * time.Second

type ServiceRegistryRepo struct {
	pool *pgxpool.Pool
}

func NewServiceRegistryRepo(pool *pgxpool.Pool) *ServiceRegistryRepo {
	return &ServiceRegistryRepo{pool: pool}
}

func (r *ServiceRegistryRepo) Register(ctx context.Context, descriptor discovery.Descriptor) (*discovery.Lease, error) {
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	if _, err := r.pool.Exec(ctx, `DELETE FROM service_instances WHERE instance_id IN (
		SELECT instance_id FROM service_instances
		WHERE (terminal_state IS NOT NULL OR lease_expires_at < NOW()) AND updated_at < NOW() - interval '24 hours'
		ORDER BY updated_at LIMIT 100
	)`); err != nil {
		return nil, fmt.Errorf("prune service registry tombstones: %w", err)
	}
	if descriptor.State == discovery.StateDraining {
		return nil, fmt.Errorf("cannot register a draining instance")
	}
	if descriptor.Scope == nil {
		descriptor.Scope = map[string]string{}
	}
	if descriptor.Capabilities == nil {
		descriptor.Capabilities = []discovery.Capability{}
	}
	descriptorJSON, fingerprint, err := encodeDescriptor(descriptor)
	if err != nil {
		return nil, err
	}
	scopeJSON, _ := json.Marshal(descriptor.Scope)
	contractsJSON, _ := json.Marshal(descriptor.Contracts)
	capabilitiesJSON, _ := json.Marshal(descriptor.Capabilities)
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin service registration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO service_catalog (service_key, service_kind, implementation, scope, contracts, capabilities)
		VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (service_key) DO NOTHING`,
		descriptor.ServiceKey, descriptor.ServiceKind, descriptor.Implementation, scopeJSON, contractsJSON, capabilitiesJSON); err != nil {
		return nil, fmt.Errorf("insert service catalog: %w", err)
	}
	var kind, implementation string
	var storedScope []byte
	if err := tx.QueryRow(ctx, `SELECT service_kind, implementation, scope FROM service_catalog WHERE service_key = $1 FOR UPDATE`, descriptor.ServiceKey).Scan(&kind, &implementation, &storedScope); err != nil {
		return nil, fmt.Errorf("lock service catalog: %w", err)
	}
	var scope map[string]string
	if err := json.Unmarshal(storedScope, &scope); err != nil {
		return nil, fmt.Errorf("decode service catalog scope: %w", err)
	}
	if kind != descriptor.ServiceKind || implementation != descriptor.Implementation || !reflect.DeepEqual(scope, descriptor.Scope) {
		return nil, ErrRegistryConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE service_catalog SET contracts = $2, capabilities = $3, last_seen_at = NOW() WHERE service_key = $1`, descriptor.ServiceKey, contractsJSON, capabilitiesJSON); err != nil {
		return nil, fmt.Errorf("update service catalog observation: %w", err)
	}
	var storedFingerprint, terminalState string
	var leaseExpires time.Time
	err = tx.QueryRow(ctx, `SELECT descriptor_fingerprint, COALESCE(terminal_state,''), lease_expires_at FROM service_instances WHERE instance_id = $1 FOR UPDATE`, descriptor.InstanceID).Scan(&storedFingerprint, &terminalState, &leaseExpires)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		var totalInstances, serviceInstances int
		if countErr := tx.QueryRow(ctx, `SELECT COUNT(*), COUNT(*) FILTER (WHERE service_key = $1 AND terminal_state IS NULL AND lease_expires_at > NOW()) FROM service_instances`, descriptor.ServiceKey).Scan(&totalInstances, &serviceInstances); countErr != nil {
			return nil, fmt.Errorf("count service registry instances: %w", countErr)
		}
		if totalInstances >= 1024 || serviceInstances >= 16 {
			return nil, ErrRegistryCapacity
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO service_instances (instance_id, service_key, descriptor_fingerprint, descriptor, reported_state, last_heartbeat_at, lease_expires_at)
			VALUES ($1,$2,$3,$4,$5,NOW(),NOW() + $6::bigint * interval '1 millisecond')
			RETURNING lease_expires_at`, descriptor.InstanceID, descriptor.ServiceKey, fingerprint, descriptorJSON, descriptor.State, registryLeaseTTL.Milliseconds()).Scan(&leaseExpires)
	case err != nil:
		return nil, fmt.Errorf("lock service instance: %w", err)
	case terminalState != "":
		return nil, ErrRegistryGone
	case storedFingerprint != fingerprint:
		return nil, ErrRegistryConflict
	default:
		err = tx.QueryRow(ctx, `
			UPDATE service_instances SET reported_state = $2, last_heartbeat_at = NOW(),
				lease_expires_at = NOW() + $3::bigint * interval '1 millisecond', updated_at = NOW()
			WHERE instance_id = $1 RETURNING lease_expires_at`, descriptor.InstanceID, descriptor.State, registryLeaseTTL.Milliseconds()).Scan(&leaseExpires)
	}
	if err != nil {
		return nil, fmt.Errorf("persist service instance: %w", err)
	}
	var registryTime time.Time
	if err := tx.QueryRow(ctx, `SELECT NOW()`).Scan(&registryTime); err != nil {
		return nil, fmt.Errorf("read registry time: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit service registration: %w", err)
	}
	return lease(registryTime, leaseExpires), nil
}

func (r *ServiceRegistryRepo) Heartbeat(ctx context.Context, instanceID string, state discovery.InstanceState) (*discovery.Lease, error) {
	if state != discovery.StateStarting && state != discovery.StateReady && state != discovery.StateNotReady {
		return nil, fmt.Errorf("invalid heartbeat state")
	}
	var registryTime, leaseExpires time.Time
	var terminal string
	err := r.pool.QueryRow(ctx, `
		UPDATE service_instances SET reported_state = $2, last_heartbeat_at = NOW(),
			lease_expires_at = NOW() + $3::bigint * interval '1 millisecond', updated_at = NOW()
		WHERE instance_id = $1 AND terminal_state IS NULL AND lease_expires_at > NOW()
		RETURNING NOW(), lease_expires_at, COALESCE(terminal_state,'')`, instanceID, state, registryLeaseTTL.Milliseconds()).Scan(&registryTime, &leaseExpires, &terminal)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if readErr := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM service_instances WHERE instance_id = $1)`, instanceID).Scan(&exists); readErr != nil {
			return nil, fmt.Errorf("check service instance: %w", readErr)
		}
		if exists {
			return nil, ErrRegistryGone
		}
		return nil, ErrRegistryNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("heartbeat service instance: %w", err)
	}
	return lease(registryTime, leaseExpires), nil
}

func (r *ServiceRegistryRepo) Drain(ctx context.Context, instanceID string) error {
	return r.markTerminal(ctx, instanceID, "draining", discovery.StateDraining)
}

func (r *ServiceRegistryRepo) Deregister(ctx context.Context, instanceID string) error {
	return r.markTerminal(ctx, instanceID, "deregistered", discovery.StateDraining)
}

func (r *ServiceRegistryRepo) markTerminal(ctx context.Context, instanceID, terminal string, state discovery.InstanceState) error {
	command, err := r.pool.Exec(ctx, `
		UPDATE service_instances SET terminal_state = CASE WHEN terminal_state = 'deregistered' THEN terminal_state ELSE $2 END, reported_state = $3, updated_at = NOW()
		WHERE instance_id = $1`, instanceID, terminal, state)
	if err != nil {
		return fmt.Errorf("mark service instance terminal: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrRegistryNotFound
	}
	return nil
}

func (r *ServiceRegistryRepo) Resolve(ctx context.Context, requirement discovery.Requirement) ([]discovery.ResolvedEndpoint, error) {
	if err := discovery.ValidateRequirement(requirement); err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT descriptor, reported_state, lease_expires_at, NOW() FROM service_instances
		WHERE service_key = $1 AND terminal_state IS NULL AND lease_expires_at > NOW()
		ORDER BY instance_id`, requirement.ServiceKey)
	if err != nil {
		return nil, fmt.Errorf("resolve service instances: %w", err)
	}
	defer rows.Close()
	var result []discovery.ResolvedEndpoint
	for rows.Next() {
		var raw []byte
		var state discovery.InstanceState
		var leaseExpires, registryTime time.Time
		if err := rows.Scan(&raw, &state, &leaseExpires, &registryTime); err != nil {
			return nil, fmt.Errorf("scan service instance: %w", err)
		}
		var descriptor discovery.Descriptor
		if err := json.Unmarshal(raw, &descriptor); err != nil {
			return nil, fmt.Errorf("decode service instance: %w", err)
		}
		descriptor.State = state
		if descriptor.Satisfies(requirement) {
			result = append(result, discovery.ResolvedEndpoint{InstanceID: descriptor.InstanceID, API: descriptor.Endpoints.API, Admin: descriptor.Endpoints.Admin, Descriptor: descriptor, RegistryTime: registryTime, LeaseExpiresAt: leaseExpires})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate service instances: %w", err)
	}
	return result, nil
}

func (r *ServiceRegistryRepo) Catalog(ctx context.Context) ([]discovery.CatalogEntry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT c.service_key, c.service_kind, c.implementation, c.scope, c.contracts, c.capabilities,
			c.first_seen_at, c.last_seen_at, i.descriptor, i.reported_state, COALESCE(i.terminal_state,''),
			i.last_heartbeat_at, i.lease_expires_at,
			(i.terminal_state IS NULL AND i.lease_expires_at > NOW() AND i.reported_state = 'ready')
		FROM service_catalog c LEFT JOIN service_instances i ON i.service_key = c.service_key
		ORDER BY c.service_key, i.instance_id`)
	if err != nil {
		return nil, fmt.Errorf("read service catalog: %w", err)
	}
	defer rows.Close()
	entries := make([]discovery.CatalogEntry, 0)
	index := make(map[string]int)
	for rows.Next() {
		var serviceKey, kind, implementation string
		var scopeRaw, contractsRaw, capabilitiesRaw []byte
		var firstSeen, lastSeen time.Time
		var descriptorRaw []byte
		var reportedState *discovery.InstanceState
		var terminal string
		var heartbeat, expires *time.Time
		var eligible *bool
		if err := rows.Scan(&serviceKey, &kind, &implementation, &scopeRaw, &contractsRaw, &capabilitiesRaw,
			&firstSeen, &lastSeen, &descriptorRaw, &reportedState, &terminal, &heartbeat, &expires, &eligible); err != nil {
			return nil, fmt.Errorf("scan service catalog: %w", err)
		}
		position, ok := index[serviceKey]
		if !ok {
			entry := discovery.CatalogEntry{ServiceKey: serviceKey, ServiceKind: kind, Implementation: implementation, FirstSeenAt: firstSeen, LastSeenAt: lastSeen, Instances: []discovery.CatalogInstance{}}
			if err := json.Unmarshal(scopeRaw, &entry.Scope); err != nil {
				return nil, err
			}
			if err := json.Unmarshal(contractsRaw, &entry.Contracts); err != nil {
				return nil, err
			}
			if err := json.Unmarshal(capabilitiesRaw, &entry.Capabilities); err != nil {
				return nil, err
			}
			entries = append(entries, entry)
			position = len(entries) - 1
			index[serviceKey] = position
		}
		if descriptorRaw != nil {
			var descriptor discovery.Descriptor
			if err := json.Unmarshal(descriptorRaw, &descriptor); err != nil {
				return nil, err
			}
			descriptor.State = *reportedState
			entries[position].Instances = append(entries[position].Instances, discovery.CatalogInstance{Descriptor: descriptor, TerminalState: terminal, LastHeartbeat: *heartbeat, LeaseExpiresAt: *expires, Eligible: *eligible})
		}
	}
	return entries, rows.Err()
}

func encodeDescriptor(descriptor discovery.Descriptor) ([]byte, string, error) {
	raw, err := json.Marshal(descriptor)
	if err != nil {
		return nil, "", fmt.Errorf("encode service descriptor: %w", err)
	}
	digest := sha256.Sum256(raw)
	return raw, hex.EncodeToString(digest[:]), nil
}

func lease(registryTime, expires time.Time) *discovery.Lease {
	remaining := int64(expires.Sub(registryTime).Seconds())
	if remaining < 0 {
		remaining = 0
	}
	return &discovery.Lease{RegistryTime: registryTime, LeaseExpiresAt: expires, LeaseRemainingSeconds: remaining}
}
