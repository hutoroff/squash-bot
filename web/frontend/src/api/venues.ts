import type { BookingReadiness, Venue, VenueCredential, VenueInput } from '../types'
import { handleResponse, expectNoContent } from './http'

const jsonHeaders = { 'Content-Type': 'application/json' }

export async function fetchVenues(chatID: number): Promise<Venue[]> {
  return handleResponse<Venue[]>(await fetch(`/api/groups/${chatID}/venues`))
}

export async function fetchVenue(chatID: number, venueID: number): Promise<Venue> {
  return handleResponse<Venue>(await fetch(`/api/groups/${chatID}/venues/${venueID}`))
}

export async function createVenue(chatID: number, input: VenueInput): Promise<Venue> {
  return handleResponse<Venue>(await fetch(`/api/groups/${chatID}/venues`, {
    method: 'POST', headers: jsonHeaders, body: JSON.stringify(input),
  }))
}

export async function updateVenue(chatID: number, venueID: number, input: VenueInput): Promise<Venue> {
  return handleResponse<Venue>(await fetch(`/api/groups/${chatID}/venues/${venueID}`, {
    method: 'PATCH', headers: jsonHeaders, body: JSON.stringify(input),
  }))
}

export async function deleteVenue(chatID: number, venueID: number): Promise<void> {
  return expectNoContent(await fetch(`/api/groups/${chatID}/venues/${venueID}`, { method: 'DELETE' }))
}

export async function fetchBookingReadiness(chatID: number, venueID: number): Promise<BookingReadiness> {
  return handleResponse<BookingReadiness>(
    await fetch(`/api/groups/${chatID}/venues/${venueID}/booking-readiness`))
}

export async function fetchCredentials(chatID: number, venueID: number): Promise<VenueCredential[]> {
  return handleResponse<VenueCredential[]>(
    await fetch(`/api/groups/${chatID}/venues/${venueID}/credentials`))
}

export async function fetchCredentialPriorities(chatID: number, venueID: number): Promise<number[]> {
  return handleResponse<number[]>(
    await fetch(`/api/groups/${chatID}/venues/${venueID}/credentials/priorities`))
}

export async function addCredential(
  chatID: number,
  venueID: number,
  cred: { login: string; password: string; priority: number; max_courts: number },
): Promise<VenueCredential> {
  return handleResponse<VenueCredential>(await fetch(`/api/groups/${chatID}/venues/${venueID}/credentials`, {
    method: 'POST', headers: jsonHeaders, body: JSON.stringify(cred),
  }))
}

export async function deleteCredential(chatID: number, venueID: number, credID: number): Promise<void> {
  return expectNoContent(
    await fetch(`/api/groups/${chatID}/venues/${venueID}/credentials/${credID}`, { method: 'DELETE' }))
}
