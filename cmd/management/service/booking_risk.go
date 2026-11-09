package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hutoroff/squash-bot/internal/bookingapi"
	"github.com/hutoroff/squash-bot/internal/models"
	"github.com/hutoroff/squash-bot/internal/sport"
)

var ErrRiskNotEligible = errors.New("duplicate-risk creation is not eligible")

type riskBookingExecutor interface {
	Observe(context.Context, bookingapi.OperationID) (*models.ManagementBookingOperation, error)
	Reconcile(context.Context, bookingapi.OperationID, bookingapi.Credentials) (*models.ManagementBookingOperation, error)
	ExecuteCreate(context.Context, *models.ManagementBookingOperation, bookingapi.Credentials) (*models.ManagementBookingOperation, error)
}

type riskCredentialLookup interface {
	GetDecryptedByID(context.Context, int64) (*DecryptedCredential, error)
}

type riskIntentStore interface {
	GetOperation(context.Context, bookingapi.OperationID) (*models.ManagementBookingOperation, error)
	GetIntent(context.Context, int64) (*models.BookingIntent, error)
	AuthorizeDuplicateRisk(context.Context, bookingapi.OperationID, int64, string, int64, string) (*models.ManagementBookingOperation, bool, error)
}

type RiskBookingService struct {
	intents     riskIntentStore
	executor    riskBookingExecutor
	credentials riskCredentialLookup
	groups      interface {
		GetByID(context.Context, int64) (*models.Group, error)
	}
	venues interface {
		GetByID(context.Context, int64) (*models.Venue, error)
	}
	scope     bookingapi.Scope
	defaultTZ *time.Location
	now       func() time.Time
}

func NewRiskBookingService(intents riskIntentStore, executor riskBookingExecutor, credentials riskCredentialLookup,
	groups interface {
		GetByID(context.Context, int64) (*models.Group, error)
	}, venues interface {
		GetByID(context.Context, int64) (*models.Venue, error)
	},
	scope bookingapi.Scope, defaultTZ *time.Location) *RiskBookingService {
	return &RiskBookingService{intents: intents, executor: executor, credentials: credentials, groups: groups, venues: venues,
		scope: scope, defaultTZ: defaultTZ, now: time.Now}
}

// AuthorizeAndDispatch permits only one explicit child after the booking ledger
// reports a terminal, stopped-executor unknown. A useful credentialed read is
// attempted first. The booking service rechecks scope/parent/account atomically.
func (s *RiskBookingService) AuthorizeAndDispatch(ctx context.Context, parentID bookingapi.OperationID, revision, credentialID, actorID int64, reason string) (*models.ManagementBookingOperation, bool, error) {
	if s == nil || s.intents == nil || s.executor == nil || s.credentials == nil || s.groups == nil || s.venues == nil || s.defaultTZ == nil || s.now == nil {
		return nil, false, fmt.Errorf("booking risk policy is not configured")
	}
	if parentID == "" || revision <= 0 || credentialID <= 0 || actorID <= 0 || len(reason) > 350 || !validManagementRiskReason(reason) {
		return nil, false, ErrRiskNotEligible
	}
	parent, err := s.intents.GetOperation(ctx, parentID)
	if err != nil {
		return nil, false, fmt.Errorf("read risk parent: %w", err)
	}
	if parent.Kind != bookingapi.OperationCreate || parent.ParentOperationID != "" || parent.State != models.ManagementBookingOperationOutcomeUnknown || parent.Revision != revision || parent.CredentialID == nil {
		return nil, false, ErrRiskNotEligible
	}
	intent, err := s.intents.GetIntent(ctx, parent.IntentID)
	if err != nil {
		return nil, false, fmt.Errorf("read risk intent: %w", err)
	}
	if intent.Scope != s.scope || intent.State != models.BookingIntentUnknown || intent.Action != bookingapi.OperationCreate {
		return nil, false, ErrRiskNotEligible
	}
	group, err := s.groups.GetByID(ctx, intent.GroupID)
	if err != nil {
		return nil, false, fmt.Errorf("read risk group: %w", err)
	}
	venue, err := s.venues.GetByID(ctx, intent.VenueID)
	if err != nil {
		return nil, false, fmt.Errorf("read risk venue: %w", err)
	}
	if group == nil || venue == nil || venue.GroupID != intent.GroupID || !riskWindowOpen(intent, venue, group, parent, s.defaultTZ, s.now()) {
		return nil, false, ErrRiskNotEligible
	}
	originalCredential, err := s.credentials.GetDecryptedByID(ctx, *parent.CredentialID)
	if err != nil || originalCredential == nil || originalCredential.VenueID != intent.VenueID {
		return nil, false, ErrRiskNotEligible
	}
	// An unknown HTTP response is not evidence the executor stopped. Only the
	// booking service's durable terminal unknown (which clears executor_id) is.
	observed, err := s.executor.Observe(ctx, parentID)
	if err != nil {
		return nil, false, fmt.Errorf("read booking risk parent: %w", err)
	}
	if !eligibleRiskParent(observed, parentID, revision) {
		return nil, false, ErrRiskNotEligible
	}
	observed, err = s.executor.Reconcile(ctx, parentID, bookingapi.Credentials{Email: originalCredential.Login, Password: originalCredential.Password})
	if err != nil {
		return nil, false, fmt.Errorf("reconcile booking risk parent: %w", err)
	}
	if !eligibleRiskParent(observed, parentID, revision) {
		return nil, false, ErrRiskNotEligible
	}
	selected, err := s.credentials.GetDecryptedByID(ctx, credentialID)
	if err != nil || selected == nil || selected.VenueID != intent.VenueID {
		return nil, false, ErrRiskNotEligible
	}
	if !riskWindowOpen(intent, venue, group, parent, s.defaultTZ, s.now()) {
		return nil, false, ErrRiskNotEligible
	}
	provenance := fmt.Sprintf("owner:%d:%s", actorID, strings.TrimSpace(reason))
	child, created, err := s.intents.AuthorizeDuplicateRisk(ctx, parentID, revision, "risk:"+string(parentID), credentialID, provenance)
	if err != nil {
		return nil, false, fmt.Errorf("allocate risk child: %w", err)
	}
	if !created {
		return child, false, nil
	} // Never dispatch twice on concurrent operator requests.
	if !riskWindowOpen(intent, venue, group, parent, s.defaultTZ, s.now()) {
		return child, true, ErrRiskNotEligible
	}
	result, err := s.executor.ExecuteCreate(ctx, child, bookingapi.Credentials{Email: selected.Login, Password: selected.Password})
	if err != nil && result == nil {
		return child, true, err // never lose the durable key on transport ambiguity
	}
	return result, true, err
}

func eligibleRiskParent(op *models.ManagementBookingOperation, parentID bookingapi.OperationID, revision int64) bool {
	return op != nil && op.OperationID == parentID && op.ParentOperationID == "" &&
		op.State == models.ManagementBookingOperationOutcomeUnknown && op.Revision == revision && !op.ResultExpired
}

// The exceptional scheduled replacement cannot extend the original [00:00,
// 00:05) local booking-opening window. Manual booking is limited to before the
// original start time; a risk replacement never creates a new slot or date.
func riskWindowOpen(intent *models.BookingIntent, venue *models.Venue, group *models.Group, parent *models.ManagementBookingOperation, defaultTZ *time.Location, now time.Time) bool {
	if intent == nil || venue == nil || group == nil || parent == nil || parent.EndProvider == "" || parent.StartProvider == "" || !venue.AutoBookingEnabled {
		return false
	}
	start, err := time.Parse(time.RFC3339Nano, parent.StartProvider)
	if err != nil {
		return false
	}
	end, err := time.Parse(time.RFC3339Nano, parent.EndProvider)
	if err != nil || !end.After(now) || !start.After(now) {
		return false
	}
	zone := defaultTZ
	if group.Timezone != "" {
		zone, err = time.LoadLocation(group.Timezone)
		if err != nil {
			return false
		}
	}
	date := time.Date(intent.GameDate.Year(), intent.GameDate.Month(), intent.GameDate.Day(), 0, 0, 0, 0, zone)
	localStart := start.In(zone)
	if localStart.Year() != date.Year() || localStart.Month() != date.Month() || localStart.Day() != date.Day() || localStart.Format("15:04") != intent.GameTime {
		return false
	}
	switch intent.Source {
	case models.BookingIntentManual:
		return intent.GameID != nil
	case models.BookingIntentScheduled:
		if !group.AutoBookingAllowed || venue.AutoBookingCourtsCount <= 0 || venue.Courts == "" ||
			(len(venue.Sports) > 0 && venue.CourtsFor(string(sport.Default)) == "") {
			return false
		}
		listed := false
		for _, preferred := range splitPreferredTimes(venue.PreferredGameTimes) {
			if preferred == intent.GameTime {
				listed = true
				break
			}
		}
		if !listed || !containsDay(venue.GameDays, int(date.Weekday())) {
			return false
		}
		localNow := now.In(zone)
		opensDate := localNow.AddDate(0, 0, venue.BookingOpensDays)
		return localNow.Hour() == 0 && localNow.Minute() < 5 && opensDate.Year() == date.Year() && opensDate.Month() == date.Month() && opensDate.Day() == date.Day()
	default:
		return false
	}
}
