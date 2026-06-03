# TODO: draft past-due reminder copy (7 / 14 / 21 / 30)

The wholesale past-due reminder mechanism is built and wired (see
[qb-reconciliation.md](qb-reconciliation.md)), but every milestone currently
renders the **same placeholder** email. This doc tracks finishing the copy.

## What exists

- One template pair: `internal/emailtemplates/html/invoice_past_due.html` and
  `text/invoice_past_due.txt`, rendered for every milestone.
- Data available to the template (`emailtemplates.InvoicePastDueData`):
  `CustomerName`, `InvoiceNumber`, `OrderNumber`, `AmountDue` (cents),
  `DueDate`, `Stage` (the milestone: 7/14/21/30), `PaymentURL`, `StoreName`,
  `StoreURL`.
- The job carries `Stage`, so the template can branch on it.

## To do

1. **Write distinct copy per milestone**, escalating in tone:
   - **7** — friendly nudge ("just a heads-up, invoice is due").
   - **14** — reminder ("now a week past due").
   - **21** — firmer ("please settle to keep your account in good standing").
   - **30** — final notice ("account may be placed on hold").
   Branch on `.Stage` inside the template, or split into per-stage templates.
2. **Confirm anchoring & exact days.** Milestones are currently *days since the
   order was placed* (so with net-7, day 7 = due date). Confirm the business
   wants 7/14/21/30-from-placed vs. days-*past-due*; adjust
   `overdueReminderMilestones` in `internal/app/orders_qb.go` if needed.
3. **Decide day-30 side effects.** Should the 30-day notice also flag the
   account for a hold / staff follow-up, beyond emailing? Out of scope for the
   reconciliation pass — capture as a separate task if wanted.
4. Keep the Rockabilly voice (plainspoken, warm, no fake urgency; em-dashes
   fine; no emoji). One CTA: "View & Pay Invoice".

## Voice reference

See the brand guidance in `CLAUDE.md` → *Brand Personality* / *Aesthetic
Direction*. Lean into: *honest, straight-up, the crew, settle up*. Avoid:
*artisanal, journey, experience, urgent*.
