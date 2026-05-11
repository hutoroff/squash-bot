package service

import "github.com/hutoroff/squash-bot/internal/management/application/ports/outbound"

// Type aliases re-exporting the canonical booking types from the outbound port package.
// This shim allows existing service code to continue using the short names until Phase 3.
type BookingSlot = outbound.BookingSlot
type BookingCourt = outbound.BookingCourt
type SlotMatchID = outbound.SlotMatchID
type BookMatchResult = outbound.BookMatchResult
type BookingServiceClient = outbound.BookingServiceClient
