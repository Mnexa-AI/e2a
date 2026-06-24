// Centralized analytics events for the onboarding funnel.
// Uses a no-op fallback so callers never need to check if analytics is loaded.

export type OnboardingEvent =
  | "domain_registration_started"
  | "domain_registration_succeeded"
  | "dns_instructions_viewed"
  | "domain_verify_attempted"
  | "domain_verify_succeeded"
  | "domain_verify_failed"
  | "agent_creation_started"
  | "agent_creation_succeeded"
  | "setup_method_selected"
  | "address_type_selected"
  | "onboarding_resume_shown"
  | "onboarding_resume_selected";

export type EventProperties = Record<string, string | number | boolean>;

type TrackFn = (event: OnboardingEvent, properties?: EventProperties) => void;

let _track: TrackFn = () => {};

/** Replace the default no-op tracker with a real implementation. */
export function setTracker(fn: TrackFn): void {
  _track = fn;
}

/** Track an onboarding event. Safe to call before setTracker — events are silently dropped. */
export function track(event: OnboardingEvent, properties?: EventProperties): void {
  _track(event, properties);
}
