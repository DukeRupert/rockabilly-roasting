# QuickBooks billing: connecting a company and going live

Wholesale invoicing does not start when you deploy. It starts when somebody
presses a button. This is that procedure.

The shop ships in **test mode**, and a fresh install stays there: the invoice
chain runs in full and records what it *would* have billed, without writing
anything to QuickBooks or emailing anyone. Nothing bills until step 6.

An install that was already invoicing through QuickBooks before migration 078
is seeded `live` instead, so upgrading a running shop does not switch its
billing off. That is decided by whether any order already carries a
`qb_invoice_id` — see `db/migrations/078_qb_billing_mode.sql`.

---

## 1. Get production credentials from Intuit

The Intuit app has two key pairs. Development keys only reach sandbox
companies; a real company needs the **Production** pair, which requires the
app profile to be filled in first — host domain, launch and disconnect URLs,
EULA and privacy policy links. Publishing to Intuit's app store is a separate
process and is **not** needed to connect a single company you control.

Register the production redirect URI on the production key list — it is a
separate list from development:

```
https://<prod-host>/admin/settings/integrations/quickbooks/callback
```

It must match `QB_REDIRECT_URI` character for character.

Register the webhook endpoint and note its verifier token:

```
https://<prod-host>/webhooks/quickbooks
```

## 2. Set the environment

In the `rr-app` container's configuration (not `~/.env` on the host):

| Variable | Notes |
|---|---|
| `QB_CLIENT_ID` / `QB_CLIENT_SECRET` | the **production** pair |
| `QB_ENVIRONMENT` | `production` |
| `QB_REDIRECT_URI` | exactly as registered above |
| `QB_WEBHOOK_VERIFIER_TOKEN` | required, or the binary refuses to boot |
| `QB_TOKEN_ENCRYPTION_KEY` | fresh 32 bytes, base64: `openssl rand -base64 32`. Never reuse another environment's key |
| `INSECURE_COOKIES` | **must not be set** |

> `QB_ENVIRONMENT` fails *toward* production. The code selects the live API
> unless the value is exactly `sandbox`, so a typo, a stray space, or an unset
> variable points the integration at a real company. Check it, don't assume it.

`QB_SALES_ITEM_ID` and `QB_SHIPPING_ITEM_ID` are **not** needed. They are set
in the admin now (step 4) and only remain as a fallback for older
deployments.

## 3. Connect the company

Someone with admin rights on the client's QuickBooks company has to grant
consent, so this is usually done on a call with them.

Admin → Settings → Integrations → **Connect**. It redirects to Intuit,
they choose the company, and it returns.

> Staff who were already signed in when the release deployed keep a
> `SameSite=Strict` session cookie and will be bounced to the login page on the
> way back from Intuit. **Log out and back in first.** This affects only
> sessions issued before the release.

Confirm the card reads **Connected** and shows the realm ID.

## 4. Choose the invoice items

Admin → Settings → Integrations → **Invoice items**.

This decides which income account wholesale revenue lands in, so it is the
bookkeeper's call, not a developer's. The picker lists the company's active
items with the account each one posts to.

Until an item is chosen, invoices cannot be created. In test mode that shows up
on the review page rather than as a failure, which is the point of doing this
before going live.

## 5. Run the proof period

Leave it in test mode for a week or two of real wholesale orders.

Admin → Settings → Integrations → **Review** lists every order and what would
have been billed: customer, terms, due date, bill-to address, total. A weekly
digest goes to `STAFF_NOTIFICATION_EMAIL` for as long as test mode is on.

Rows are flagged when they need a human. The one that matters most is **"No
matching QuickBooks customer"** — the company's existing customer records are
matched by email first, then display name, so an account whose QuickBooks
record has no email or a different company spelling will not match. Going live
without fixing it creates a duplicate customer in their books.

**Fix matching in QuickBooks, not in Hiri.** There is no way to link an account
to a QuickBooks customer from the admin; the practical fix is putting the right
email on their QuickBooks customer record.

Also worth checking during this period:

- Do any orders show **"QuickBooks already has an invoice numbered …"**? That
  means someone billed it by hand.
- Does the client discount wholesale orders? Discounts and tax are **not** on
  QuickBooks invoices — the total is lines plus shipping only, so a discounted
  order would be over-billed.

## 5a. Decide who is actually billed

Automated billing follows the customer's **billing method**, not the billing
mode. Only accounts set to **ACH** or **Credit card** are invoiced
automatically; an account on **Manual** is left alone in both test mode and
live, because manual means nobody has an invoicing and payment agreement with
them and their invoices are raised by hand.

That is deliberate and it is load-bearing: **every wholesale account starts on
Manual.** Going live therefore bills nobody until you begin moving accounts
over, which is the point — an automated invoice with a pay-now button is a
change to a commercial relationship, not a feature to switch on for sixty
businesses at once.

The order of operations for each account is: agree it with the customer, then
set their billing method on the customer page, and from their next order they
are invoiced automatically.

Orders on manual accounts still appear on the review page, badged **Manual**
and counted under "Invoice by hand". They are not a problem to fix — they are
the list of orders somebody has to invoice, and **Bill now** on such a row is
the deliberate way to have QuickBooks do it once.

## 6. Go live

Admin → Settings → Integrations → Billing mode → **Go live**. It asks for
confirmation and records who did it in the audit log.

From that moment new wholesale orders are invoiced in QuickBooks and emailed to
the customer.

**Going live does not bill the backlog.** Orders recorded during the proof
period are not invoiced retrospectively — almost always correct, because those
orders were invoiced by hand while the proof period ran. To bill one
deliberately, use **Bill now** on its row in the review list. Check first that
it was not already invoiced by hand: our duplicate guard matches on the Hiri
order number, so a hand-written invoice carrying the client's own numbering is
invisible to it and the customer would be billed twice.

The first invoice will create a **"Net 7"** term in the company, because it is
not one of QuickBooks' stock terms. Tell the bookkeeper to expect it.

If nothing at all is invoiced after going live, that is almost certainly step
5a: every account is still on Manual. Check a customer's billing method before
assuming the integration is broken.

## 7. Watch the first day

- Admin → Settings → Integrations shows the connection and the refresh-token
  expiry. Intuit expires the refresh token 100 days after last use; a daily job
  warns staff a week out.
- Dead jobs surface under the admin jobs page. A permanently failed invoice
  also emails `STAFF_NOTIFICATION_EMAIL`.
- Check the first invoice in QuickBooks itself: right customer, right income
  account, terms and due date as expected.
- **Expect a burst of customer updates on the first live run.** Until this
  release the stored QuickBooks link was written but never read back, so the
  "re-sync a customer whose details changed" path had never executed. It now
  can, and because those customers have no recorded sync time, the first run
  treats them all as changed — pushing each account's local details over its
  QuickBooks record once. That is the intended behaviour finally happening,
  not a fault, but it is a lot of writes at once and worth knowing before you
  see them.

## Backing out

**Switch back to test mode** from the same panel. New orders stop billing
immediately; invoices already in QuickBooks are untouched, and reconciliation
of them carries on.

Orders billed while you were live keep their QuickBooks invoice, and Hiri's
manual invoice flow will not touch them in either mode — recording a payment
against such an order is refused outright ("this order is managed by
QuickBooks"). The way to settle one is **Mark as paid** on the order, or
recording the payment in QuickBooks itself.

Worth knowing, and not specific to test mode: **Mark as paid** settles the
order in Hiri and sends nothing to QuickBooks. The QBO invoice goes on showing
the full balance owed until someone applies the payment there. If you are
reconciling a month that spans a go-live or a back-out, that is the gap to
look for.

**Disconnect** drops the OAuth connection entirely and stops all automated
invoicing until someone reconnects. Nothing is deleted on Intuit's side.

Neither voids anything. An invoice sent in error has to be voided in
QuickBooks by hand.
