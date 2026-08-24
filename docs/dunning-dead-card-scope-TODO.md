# TODO: dead-card records are per-subscription, but a card is per-customer

The automated dunning ladder (shipped in the `subscriptions/automated-dunning`
branch, PR #9) refuses to re-charge a card the issuer permanently declined. It
remembers those cards **per subscription**. A Stripe payment method belongs to
the **customer**, so a customer with more than one subscription rediscovers the
same dead card once per subscription.

Known and accepted at merge; captured here rather than fixed, because moving the
record to the customer is a data-model decision with its own edge cases.

## What exists today

Dunning state lives entirely in `subscriptions.metadata` (jsonb) — no dedicated
columns, no migration.

- `domain.SubscriptionMetaDunningDeadPaymentMethods` — the set of Stripe payment
  method IDs known dead on this subscription's current past-due run.
- `Subscription.DunningDeadPaymentMethods()` (`internal/domain/subscription.go:331`)
  — reads the set, merging the superseded single-card key.
- `Subscription.DunningChargeBlocked(pm)` (`:309`) — true when `pm` is in the set.
- `Subscription.DunningHasDeadCard()` (`:294`) — set non-empty, or latched.
- `store.SetDunningHardDecline(..., deadPaymentMethods []string)`
  (`internal/store/subscriptions.go:252`) — writes the whole set.
- `store.ClearDunning` (`:353`) — wipes it on a successful charge.
- `RenewalService.pickRenewalPaymentMethod(ctx, stripeCustomerID, avoid []string)`
  — skips every card in `avoid`, returning one only when nothing else is attached.

Three places decide what to charge:

| Site | Behaviour |
|---|---|
| `RenewSubscription` (`internal/app/renewal.go:484`, gate at `:506`) | picks with the subscription's dead set, then refuses if the resolved card is in it |
| `RenewBatch` (`internal/app/renewal.go:754`, picks at `:847`) | drops every member with `DunningHasDeadCard()`, then picks with `avoid = nil` |
| `RenewalSchedulerWorker.Work` (`internal/jobs/renewal_scheduler.go:82`) | routes `DunningHasDeadCard()` subscriptions to solo renewals instead of batches |

## The problem

Subscription A records `pm_dead`. Subscription B, same customer, has no dunning
history — so `DunningHasDeadCard()` is false for it, the scheduler batches it,
and `RenewBatch` picks a payment method with **nothing to avoid**. It resolves
the customer's Stripe default, which is still `pm_dead`, and charges it.

Two costs:

1. **A card-network fine per attempt.** Avoiding exactly this is why
   `payments.DeclineError` and the whole hard-decline path exist — see the
   rationale in `internal/platform/payments/decline.go`.
2. **The customer is told the wrong thing.** B enters its own dunning ladder and
   emails a past-due notice for a card the customer may already have replaced.

It is bounded and self-correcting: B latches on first contact and thereafter
behaves correctly. The cost is one wasted attempt per sibling subscription, not
an unbounded loop. That is why it did not block the merge.

The batch path is the sharp edge. A sibling routed to a *solo* renewal would at
least gate on its own (empty) set and get one clean attempt; a sibling routed
into a *batch* charges through a path that has no avoid-list at all.

## To do

1. **Decide where the dead-card set lives.** Options, roughly in order of
   increasing scope:
   - **Customer metadata / column.** Hoist the set to the customer, keeping the
     per-subscription latch (which is about *this* subscription's ladder, not
     about the card). `pickRenewalPaymentMethod` then takes the customer's set
     in every path, including `RenewBatch`, and the batch's `avoid = nil` special
     case disappears. Simplest thing that closes the hole.
   - **A `dead_payment_methods` table** keyed by customer + Stripe PM ID, with
     the decline code and a timestamp. Better if we ever want to report on it,
     or expire entries by age.
   - **Ask Stripe instead of remembering.** A `payment_method.detached` /
     `payment_method.attached` webhook pair could keep the picture current
     without us storing a set at all. More moving parts, and it fails open if a
     webhook is missed.
2. **Answer: when does a customer-level dead card clear?** This is the real
   design question and the reason it wasn't done inline. Per-subscription, the
   answer is easy — `ClearDunning` on a successful charge. Per-customer it is
   not: one subscription recovering on `pm_new` says nothing about whether
   `pm_dead` came back to life. Candidates: never (until the PM is detached in
   Stripe); on `payment_method.detached`; after N days. Picking "never" is
   defensible and simplest, but means a card reinstated by the issuer stays
   refused.
3. **Decide whether the latch stays per-subscription.** It should. It means "we
   are not charging *this subscription*", and roughly a dozen readers — the
   admin badge and status line, the reminder email copy, the update-card page —
   depend on that meaning. Only the *card* record is customer-scoped.
4. **Simplify `RenewBatch` once the set is customer-scoped.** The
   `DunningHasDeadCard()` filter at `renewal.go:754` and the scheduler's solo
   routing at `renewal_scheduler.go:82` exist only because the batch path cannot
   resolve a per-subscription avoid-list. With a customer-level set the batch can
   pass it directly and both special cases can probably go.
5. **Extend the story tests.** `TestDunningStoryDeadCardThenReplacement` and
   `TestDunningStoryTwoDeadCards` (`internal/app/renewal_dunning_test.go`) walk
   one subscription. Add a sibling-subscription story: A latches on `pm_dead`,
   B renews, B must not charge `pm_dead`. Follow the existing habit on this
   code — revert the fix and confirm the new test fails before trusting it.

## Related, smaller, same area

- **`SetDunningHardDecline` is last-write-wins on the set**
  (`internal/store/subscriptions.go:252`): the caller merges and the store
  overwrites. Two overlapping renewal attempts could drop a card. Very hard to
  reach given River's `ByArgs + ByPeriod: 24h` uniqueness on renewal jobs, and
  both writers would almost certainly be recording the same card. A jsonb `||`
  concat with server-side dedup would close it outright.
- **A subscription with no card on file at all leaves the latch released**, so
  the admin renders a next-attempt date for a charge that cannot happen.
  Display-only — routing still keys on the dead-card set, so nothing charges and
  nothing batches.

## Why this took six review passes to get here

Worth knowing before touching this code. Five separate review rounds each found
a different variant of one rule — *a card the issuer permanently declined must
never be charged again* — because each fix held in one code path and missed
another. The two things that finally made it stable:

- `dunningVerdictFor(subscription, card, error)` in `internal/app/renewal.go` is
  a **pure function**. The decision every one of those bugs lived in used to be
  reachable only through a transaction and a Stripe client, so it got tested a
  field at a time while the sequence went uncovered.
- The story tests walk a whole customer's sequence rung by rung rather than
  asserting on pieces.

Any change here should keep the decision pure and extend the stories, not add a
sixth predicate check at a seventh call site.
