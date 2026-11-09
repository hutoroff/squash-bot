package main

import (
	"context"
	"testing"
)

func TestManagementHealthStaysUnreadyBeforeDatabaseInitialization(t *testing.T) {
	health := &managementHealthSource{}
	health.businessListening.Store(true)

	readiness := health.readiness(context.Background())
	if readiness.Ready {
		t.Fatal("startup health reported ready without a migrated database")
	}
	if readiness.Checks["database"] {
		t.Fatal("database check passed without a pool")
	}
	if !readiness.Checks["business_listener"] {
		t.Fatal("business listener check was not retained")
	}
	if !readiness.Checks["draining"] {
		t.Fatal("new health source reported draining")
	}
}

func TestManagementHealthDrainFailsReadiness(t *testing.T) {
	health := &managementHealthSource{}
	health.draining.Store(true)

	readiness := health.readiness(context.Background())
	if readiness.Ready {
		t.Fatal("draining health reported ready")
	}
	if readiness.Checks["draining"] {
		t.Fatal("draining check passed")
	}
}
