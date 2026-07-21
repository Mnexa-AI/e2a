# Wildcard MX Label

## Goal

Replace the raw `inbound_mx_wildcard` purpose shown in DNS setup with a short, user-facing explanation.

## Design

- Display `Route email for all subdomains` for the `inbound_mx_wildcard` DNS record.
- Apply the label consistently in both the domain details and onboarding DNS setup views.
- Preserve the existing raw-purpose fallback for unknown future DNS record purposes.
- Make no API or DNS behavior changes.

## Verification

Add or update focused web tests to confirm the wildcard record renders the new label and the internal purpose key is not shown.
