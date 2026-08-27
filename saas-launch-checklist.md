# SaaS Launch Readiness Checklist (First 50 Customers)

Goal: nothing embarrassing breaks, and you can recover when it does. Feature completeness is not the bar.

Track each project in its own column. Mark items `[x]` done, `[-]` deliberately skipped, `[ ]` open.

| Legend | |
|---|---|
| P1 | Must be done before first paying customer |
| P2 | Must be done before cohort 2 (customers 11 to 50) |
| P3 | Nice to have; build when a customer asks |

---

## 1. Identity and Access

| Pri | Item | Project A | Project B | Notes |
|---|---|---|---|---|
| P1 | Signup with email verification | [ ] | [ ] | |
| P1 | Password reset flow (self-serve) | [ ] | [ ] | |
| P1 | Session expiry and logout everywhere | [ ] | [ ] | |
| P1 | Magic link or OAuth login | [ ] | [ ] | Reduces password support tickets |
| P1 | Owner / member roles only | [ ] | [ ] | Defer finer permissions |
| P2 | Invite teammates to an account | [ ] | [ ] | |
| P3 | SSO / SAML | [-] | [-] | Skip at 50 |

## 2. Multi-Tenancy and Data Safety

| Pri | Item | Project A | Project B | Notes |
|---|---|---|---|---|
| P1 | Every query scoped by tenant ID (audited) | [ ] | [ ] | The one bug that kills a SaaS |
| P1 | Automated test proving tenant A cannot read tenant B | [ ] | [ ] | |
| P1 | Nightly offsite backups | [ ] | [ ] | |
| P1 | Restore rehearsed at least once, procedure documented | [ ] | [ ] | Date of last rehearsal: |
| P1 | Reversible migrations with a rollback step | [ ] | [ ] | |
| P2 | Data export for customers | [ ] | [ ] | Cheap, builds trust |
| P2 | Account / data deletion path | [ ] | [ ] | Required by privacy policy |

## 3. Billing

| Pri | Item | Project A | Project B | Notes |
|---|---|---|---|---|
| P1 | Stripe (or equivalent) subscription integration | [ ] | [ ] | |
| P1 | Webhooks handled idempotently | [ ] | [ ] | Store event IDs, ignore duplicates |
| P1 | Customer portal: update card, cancel, view invoices | [ ] | [ ] | |
| P1 | Trial or free tier with clear end behavior | [ ] | [ ] | |
| P1 | Failed payment dunning emails | [ ] | [ ] | Most commonly forgotten item |
| P2 | Grace period and downgrade logic on non-payment | [ ] | [ ] | |
| P2 | Receipts emailed automatically | [ ] | [ ] | |
| P3 | Usage-based or metered billing | [-] | [-] | Only if the product needs it |

## 4. Operations and Observability

| Pri | Item | Project A | Project B | Notes |
|---|---|---|---|---|
| P1 | Error tracking with alerts that reach your phone | [ ] | [ ] | |
| P1 | External uptime monitor (outside your infra) | [ ] | [ ] | |
| P1 | Structured logs with request ID and tenant ID | [ ] | [ ] | Reconstruct a session from a support email |
| P1 | Deploy in under 10 minutes with known rollback | [ ] | [ ] | |
| P1 | Secrets out of the repo, rotated once | [ ] | [ ] | |
| P2 | Status page (static is fine) | [ ] | [ ] | |
| P2 | Rate limiting on public endpoints and signup | [ ] | [ ] | |
| P2 | Feature flags (table-driven is enough) | [ ] | [ ] | Ship dark, enable per customer |
| P2 | Dashboards: error rate, latency, job queue depth | [ ] | [ ] | |
| P2 | Background job retries and dead-letter visibility | [ ] | [ ] | |
| P3 | Horizontal scaling | [-] | [-] | Skip at 50 |

## 5. Customer-Facing Essentials

| Pri | Item | Project A | Project B | Notes |
|---|---|---|---|---|
| P1 | Lifecycle emails: welcome, verify, receipt, trial ending | [ ] | [ ] | |
| P1 | Obvious support channel (email is fine) | [ ] | [ ] | |
| P1 | Onboarding reaches first meaningful result in one session | [ ] | [ ] | |
| P1 | Empty states designed, not blank | [ ] | [ ] | |
| P1 | Admin read-only view of any customer account | [ ] | [ ] | For debugging, logged |
| P2 | In-app changelog or "what's new" | [ ] | [ ] | |
| P2 | Help docs for the 5 most common tasks | [ ] | [ ] | |
| P3 | Public API | [-] | [-] | |
| P3 | White-labeling | [-] | [-] | |

## 6. Legal and Trust

| Pri | Item | Project A | Project B | Notes |
|---|---|---|---|---|
| P1 | Terms of service | [ ] | [ ] | Boilerplate is fine |
| P1 | Privacy policy | [ ] | [ ] | |
| P1 | HTTPS everywhere, HSTS on | [ ] | [ ] | |
| P2 | Security basics: CSRF, CSP headers, dependency scan in CI | [ ] | [ ] | |
| P2 | Cookie / consent notice if applicable | [ ] | [ ] | |

## 7. Launch Mechanics

| Pri | Item | Project A | Project B | Notes |
|---|---|---|---|---|
| P1 | Cohort plan: 5 to 10 customers per wave | [ ] | [ ] | |
| P1 | Personal conversation with each of the first 10 | [ ] | [ ] | |
| P1 | Known-issues doc, shared openly | [ ] | [ ] | |
| P1 | Feedback capture (tag, log, review weekly) | [ ] | [ ] | |
| P2 | Pricing page and checkout tested end to end by someone else | [ ] | [ ] | |
| P2 | Launch-day runbook: who watches what, rollback trigger | [ ] | [ ] | |
| P2 | Post-cohort review before opening the next wave | [ ] | [ ] | |

---

## Go / No-Go Gate

Do not admit cohort 1 until every P1 is `[x]` or `[-]` with a written reason.
Do not admit cohort 2 until every P2 is `[x]` or `[-]` with a written reason.

| Gate | Project A | Project B |
|---|---|---|
| All P1 closed | [ ] | [ ] |
| All P2 closed | [ ] | [ ] |
| Last restore rehearsal date | | |
| Last full deploy + rollback test | | |
