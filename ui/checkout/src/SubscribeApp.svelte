<script lang="ts">
  import { tick } from 'svelte';
  import type { Stripe, StripeElements } from '@stripe/stripe-js';
  import { getStripe, createElements } from './lib/stripe';
  import {
    createSubscribePaymentIntent,
    confirmSubscription,
    getSubscribeContext,
    getSubscribeCatalog,
    type SubscribePaymentIntentResponse,
    type SubscribeCatalogResponse,
    type SubscribeCatalogVariant,
    type SubscribeCatalogPlan,
  } from './lib/subscribe-api';
  import { formatCents } from './lib/format';

  // One line of the box, as the mount div's data-lines attribute carries it —
  // see storefront.SubscribeLineProps. Read on mount; after an add or remove,
  // each line's price is read again from the refreshed box summary (see
  // refreshSummary). Everything else is local state.
  interface BoxLine {
    plan_id: string;
    variant_id: string;
    quantity: number;
    // Null until the server has priced the line: a line just added from the
    // picker has no price of the server's yet, and one computed here would be
    // a second price computation that could disagree with the charge.
    unit_price: number | null;
    base_price: number;
    product_title: string;
    product_slug: string;
    variant_label: string;
    thumbnail_url?: string;
    plan_name: string;
    interval: string;
    interval_count: number;
  }

  interface Props {
    lines: BoxLine[];
    stripeKey: string;
  }

  let { lines: initialLines, stripeKey }: Props = $props();

  let lines = $state<BoxLine[]>(initialLines);

  type Step = 'form' | 'confirmation';

  let step = $state<Step>('form');
  // Every subscription the signup started, named for the success screen.
  let confirmedSubscriptionIds = $state<string[]>([]);
  // 'active' — subscriptions exist; 'processing' — payment is settling and
  // the payment_intent.succeeded webhook will activate them server-side.
  let confirmationStatus = $state<'active' | 'processing'>('active');
  // True while we're handling a Stripe redirect-back (async payment methods).
  // Suppresses the form and shows a finalizing state instead.
  let finalizing = $state(false);

  // Form fields
  let email = $state('');
  let firstName = $state('');
  let lastName = $state('');
  let line1 = $state('');
  let line2 = $state('');
  let city = $state('');
  let addressState = $state('');
  let postalCode = $state('');
  let country = $state('US');

  // Stripe state
  let stripe = $state<Stripe | null>(null);
  let elements = $state<StripeElements | null>(null);
  let clientSecret = $state('');
  let stripeReady = $state(false);
  // Server-computed charge breakdown (item subtotal + shipping + tax, and
  // each line's own price), known once the PI is created. Until then the
  // page shows the lines' preview prices: from the mount data, and after an
  // add or remove from the refreshed box summary.
  let totals = $state<SubscribePaymentIntentResponse | null>(null);

  // "Add another item" picker
  let pickerOpen = $state(false);
  let catalog = $state<SubscribeCatalogResponse | null>(null);
  let catalogError = $state('');
  // What the picker refused, said rather than silently clamped: the server
  // refuses a merged quantity over the cap, and so does this.
  let pickerError = $state('');
  let pickerVariantId = $state('');
  let pickerPlanId = $state('');
  let pickerQuantity = $state(1);

  // UI state
  let processing = $state(false);
  let error = $state('');
  let formValid = $state(false);

  const inputClasses =
    'paper-input w-full border-2 border-ink bg-cream-hi px-3.5 py-2.5 font-oswald text-sm text-ink placeholder:text-chrome-deep focus:outline-none';
  const labelClasses =
    'font-oswald font-bold text-ink text-[11px] mb-2 block';
  const labelStyle = 'letter-spacing:0.2em; text-transform:uppercase;';
  const inputStyle = 'letter-spacing:0.04em;';

  // Validate required fields
  $effect(() => {
    formValid =
      email.trim() !== '' &&
      firstName.trim() !== '' &&
      lastName.trim() !== '' &&
      line1.trim() !== '' &&
      city.trim() !== '' &&
      addressState.trim() !== '' &&
      postalCode.trim() !== '';
  });

  // Initialize Stripe when form becomes valid
  let stripeInitialized = false;
  $effect(() => {
    if (formValid && !stripeInitialized && !finalizing && step === 'form') {
      stripeInitialized = true;
      initStripe();
    }
  });

  let redirectHandled = false;
  $effect(() => {
    if (!redirectHandled) {
      redirectHandled = true;
      handleRedirectBack();
    }
  });

  // baseURL is this page's canonical address without Stripe's redirect-back
  // query params, rebuilt from the current lines every time the box changes
  // — used both to reset the URL after a redirect and to keep it describing
  // the box after an add or remove.
  //
  // Every line is a repeated `line=<plan>:<variant>:<qty>` param.
  function boxQuery(): string {
    const params = new URLSearchParams();
    for (const l of lines) {
      params.append('line', `${l.plan_id}:${l.variant_id}:${l.quantity}`);
    }
    return params.toString();
  }

  function baseURL(): string {
    const qs = boxQuery();
    return qs ? `${window.location.pathname}?${qs}` : window.location.pathname;
  }

  // refreshSummary swaps in the server's summary of the box after an add or
  // remove. The summary at the top of the page is server-rendered — each line,
  // the per-delivery total, the separate-shipments notice — and nothing here
  // may price it, so the server renders it again
  // for the new lines: GET /subscribe/box is the page's own summary component.
  // The fragment's data-lines also carries each line's price, which is how a
  // line just added gets the server's price without waiting for the payment
  // step.
  //
  // A later change supersedes an earlier fetch still in flight. If the server
  // refuses the box (a line that can no longer be offered) or cannot be
  // reached, the page is loaded for the new URL, which answers the same way the
  // page always does.
  let summarySeq = 0;
  async function refreshSummary() {
    const seq = ++summarySeq;
    const qs = boxQuery();
    try {
      const res = await fetch(`/subscribe/box${qs ? `?${qs}` : ''}`, {
        credentials: 'same-origin',
        headers: { Accept: 'text/html' },
      });
      if (seq !== summarySeq) return;
      // Refused (a line that can no longer be offered), or redirected
      // somewhere fetch followed: either way the page for the new URL is the
      // answer.
      if (!res.ok || res.redirected) {
        window.location.assign(baseURL());
        return;
      }
      const html = await res.text();
      if (seq !== summarySeq) return;
      const parsed = document.createElement('template');
      parsed.innerHTML = html;
      const fresh = parsed.content.querySelector<HTMLElement>('#subscribe-box-summary');
      const current = document.getElementById('subscribe-box-summary');
      if (!fresh || !current) return;
      current.replaceWith(fresh);
      const priced: BoxLine[] = JSON.parse(fresh.dataset.lines || '[]');
      for (const p of priced) {
        const line = lines.find(
          (l) =>
            l.plan_id === p.plan_id &&
            l.variant_id === p.variant_id,
        );
        if (line) line.unit_price = p.unit_price;
      }
    } catch {
      if (seq === summarySeq) window.location.assign(baseURL());
    }
  }

  // handleRedirectBack runs once on mount. If the URL carries Stripe's
  // redirect-back query params (payment_intent + redirect_status) the
  // customer is returning from an async payment method. The order was
  // pre-created server-side at PaymentIntent time, so finalizing is a single
  // confirm call — and even if it fails here, the payment_intent.succeeded
  // webhook activates the subscriptions without us.
  async function handleRedirectBack() {
    const params = new URLSearchParams(window.location.search);
    const redirectPI = params.get('payment_intent');
    const redirectStatus = params.get('redirect_status');

    if (!redirectPI) {
      // Normal mount — prefill from the signed-in customer if we have one.
      loadPrefill();
      return;
    }

    if (redirectStatus === 'failed') {
      // The redirect-based method declined. Drop the query params so a
      // refresh doesn't re-enter this branch and let the customer retry.
      window.history.replaceState({}, '', baseURL());
      error = 'Payment was not completed. You can try another method below.';
      return;
    }

    // succeeded or processing — finalize against the pre-created order.
    finalizing = true;
    try {
      const result = await confirmSubscription({ payment_intent_id: redirectPI });
      confirmedSubscriptionIds = result.subscription_ids || (result.subscription_id ? [result.subscription_id] : []);
      confirmationStatus = result.status === 'processing' ? 'processing' : 'active';
      step = 'confirmation';
      window.history.replaceState({}, '', baseURL());
    } catch {
      // Payment is in Stripe's hands and the webhook will finish activation
      // server-side — show the truthful processing state, not a dead end.
      confirmationStatus = 'processing';
      step = 'confirmation';
      window.history.replaceState({}, '', baseURL());
    } finally {
      finalizing = false;
    }
  }

  // loadPrefill seeds the form from the signed-in customer's contact + default
  // address. Guests (or any failure) leave the fields blank. Only fills empties
  // so it never clobbers something the customer already started typing.
  async function loadPrefill() {
    try {
      const { prefill } = await getSubscribeContext();
      if (!prefill) return;
      if (!email) email = prefill.email ?? '';
      if (!firstName) firstName = prefill.first_name ?? '';
      if (!lastName) lastName = prefill.last_name ?? '';
      if (!line1) line1 = prefill.line1 ?? '';
      if (!line2) line2 = prefill.line2 ?? '';
      if (!city) city = prefill.city ?? '';
      if (!addressState) addressState = prefill.state ?? '';
      if (!postalCode) postalCode = prefill.postal_code ?? '';
      if (prefill.country) country = prefill.country;
    } catch {
      // Prefill is a convenience — never block the form on it.
    }
  }

  // The PI being abandoned when the address or the box changes and a new PI
  // is created. Sent to the server so the orphaned PI (and its pre-created
  // order) get cancelled instead of lingering.
  let previousPaymentIntentId = '';

  // One payment intent is created at a time. A box edit or address change
  // that lands while a create is in flight only marks it stale: two creates
  // racing would let whichever answered last decide what the card is charged
  // for — possibly the box before the edit — and the earlier intent would be
  // sent no cancellation. When a stale create answers, its intent becomes the
  // previous one and a fresh create goes out for the current box, so every
  // abandoned intent is cancelled by its successor.
  let intentInFlight = false;
  let intentStale = false;

  async function initStripe() {
    if (intentInFlight) {
      intentStale = true;
      return;
    }
    intentInFlight = true;
    try {
      stripe = await getStripe(stripeKey);
      if (!stripe) {
        error = 'Failed to load payment system';
        return;
      }

      let piResponse: SubscribePaymentIntentResponse;
      for (;;) {
        intentStale = false;
        piResponse = await createSubscribePaymentIntent({
          lines: lines.map((l) => ({
            plan_id: l.plan_id,
            variant_id: l.variant_id,
            quantity: l.quantity,
          })),
          email,
          first_name: firstName,
          last_name: lastName,
          line1,
          line2: line2 || undefined,
          city,
          state: addressState,
          postal_code: postalCode,
          country,
          previous_payment_intent_id: previousPaymentIntentId || undefined,
        });
        previousPaymentIntentId = '';
        if (!intentStale) break;
        // The box or address changed while this was being created: it is
        // already the wrong charge. Hand it to the next create to cancel.
        previousPaymentIntentId = piResponse.client_secret.split('_secret')[0];
        if (!formValid) {
          // Nothing valid to charge for yet; the next valid change creates
          // one, and cancels this on the way.
          stripeInitialized = false;
          return;
        }
      }

      totals = piResponse;
      // Nothing computed in the browser: every line's price shown from here
      // on is the server's. Matched on plan and variant together — the key the
      // server merges lines on — because one variant can be in the box on two
      // plans at two prices.
      for (const priced of piResponse.lines) {
        const line = lines.find(
          (l) =>
            l.plan_id === priced.plan_id &&
            l.variant_id === priced.variant_id,
        );
        if (line) line.unit_price = priced.unit_price;
      }
      clientSecret = piResponse.client_secret;
      stripeReady = true;
      await tick();

      elements = createElements(stripe, clientSecret);
      const paymentElement = elements.create('payment');
      paymentElement.mount('#stripe-subscribe-payment');
    } catch (e: any) {
      error = e.message || 'Failed to initialize payment';
      stripeInitialized = false;
    } finally {
      intentInFlight = false;
    }
  }

  async function handleSubmit(e: Event) {
    e.preventDefault();
    if (!stripe || !elements) return;

    processing = true;
    error = '';

    try {
      // If address changed since PI was created, create a new one
      if (!clientSecret) {
        await initStripe();
        if (!clientSecret) return;
      }

      const { error: stripeError, paymentIntent } = await stripe.confirmPayment({
        elements,
        confirmParams: {
          return_url: window.location.href,
        },
        redirect: 'if_required',
      });

      if (stripeError) {
        error = stripeError.message || 'Payment failed';
        processing = false;
        return;
      }

      if (!paymentIntent || (paymentIntent.status !== 'succeeded' && paymentIntent.status !== 'processing')) {
        error = 'Payment was not completed. Please try again.';
        processing = false;
        return;
      }

      try {
        const result = await confirmSubscription({ payment_intent_id: paymentIntent.id });
        confirmedSubscriptionIds = result.subscription_ids || (result.subscription_id ? [result.subscription_id] : []);
        confirmationStatus = result.status === 'processing' ? 'processing' : 'active';
      } catch {
        // The charge went through; the payment_intent.succeeded webhook will
        // activate the subscriptions server-side. Show the truthful
        // processing state instead of an error the customer can't act on.
        confirmationStatus = 'processing';
      }
      step = 'confirmation';
    } catch (e: any) {
      error = e.message || 'Failed to complete subscription';
    } finally {
      processing = false;
    }
  }

  // resetPaymentIntent abandons whatever PI is in flight (remembering it for
  // cancellation) and recreates one for the current lines + address. Shared
  // by the address-blur handler and every box edit — a line added or
  // removed is exactly the same kind of change as an edited address: what is
  // being charged for moved, so the intent has to move with it.
  function resetPaymentIntent() {
    if (clientSecret) {
      previousPaymentIntentId = clientSecret.split('_secret')[0];
    }
    stripeReady = false;
    clientSecret = '';
    totals = null;
    elements = null;
    // Whatever a create in flight returns now describes the old box or
    // address; see initStripe.
    if (intentInFlight) intentStale = true;
    if (formValid) {
      stripeInitialized = true;
      initStripe();
    } else {
      stripeInitialized = false;
    }
  }

  // Re-create payment intent when address changes after initial creation
  let addressKey = $derived(
    `${email}-${firstName}-${lastName}-${line1}-${line2}-${city}-${addressState}-${postalCode}-${country}`,
  );
  let lastAddressKey = '';

  function handleAddressBlur() {
    if (!formValid || !stripeInitialized) return;
    if (addressKey !== lastAddressKey) {
      lastAddressKey = addressKey;
      resetPaymentIntent();
    }
  }

  // --- The box: add and remove lines ---

  async function openPicker() {
    pickerOpen = true;
    if (catalog) return;
    try {
      catalog = await getSubscribeCatalog();
      if (catalog.plans.length > 0) pickerPlanId = catalog.plans[0].plan_id;
      if (catalog.variants.length > 0) pickerVariantId = catalog.variants[0].variant_id;
    } catch (e: any) {
      catalogError = e.message || 'Failed to load items';
    }
  }

  function closePicker() {
    pickerOpen = false;
  }

  function addPickedItem() {
    pickerError = '';
    if (!catalog || !pickerVariantId || !pickerPlanId) return;
    const variant = catalog.variants.find((v: SubscribeCatalogVariant) => v.variant_id === pickerVariantId);
    const plan = catalog.plans.find((p: SubscribeCatalogPlan) => p.plan_id === pickerPlanId);
    if (!variant || !plan) return;
    if (!Number.isInteger(pickerQuantity) || pickerQuantity < 1 || pickerQuantity > 10) {
      pickerError = 'Choose a quantity from 1 to 10.';
      return;
    }

    const existing = lines.find((l) => l.variant_id === variant.variant_id && l.plan_id === plan.plan_id);
    if (existing) {
      // Refused, never clamped: the server refuses the same merged total.
      if (existing.quantity + pickerQuantity > 10) {
        pickerError = `That would make ${existing.quantity + pickerQuantity} of one item. The most is 10.`;
        return;
      }
      existing.quantity += pickerQuantity;
    } else {
      if (lines.length >= 10) {
        pickerError = 'A subscription can hold at most 10 items.';
        return;
      }
      lines.push({
        plan_id: plan.plan_id,
        variant_id: variant.variant_id,
        quantity: pickerQuantity,
        // Priced by the server when the payment is next set up.
        unit_price: null,
        base_price: variant.base_price,
        product_title: variant.product_title,
        product_slug: '',
        variant_label: variant.variant_label,
        plan_name: plan.name,
        interval: '',
        interval_count: 1,
      });
    }
    pickerQuantity = 1;
    pickerOpen = false;
    resetPaymentIntent();
    window.history.replaceState({}, '', baseURL());
    refreshSummary();
  }

  function removeLine(index: number) {
    if (lines.length <= 1) return;
    lines.splice(index, 1);
    resetPaymentIntent();
    window.history.replaceState({}, '', baseURL());
    refreshSummary();
  }
</script>

{#if finalizing}
  <div class="mt-8 border-2 border-ink bg-cream-hi p-8 text-center shadow-stamp">
    <svg class="mx-auto size-6 animate-spin text-ink mb-4" fill="none" viewBox="0 0 24 24" aria-hidden="true">
      <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="3"></circle>
      <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"></path>
    </svg>
    <p class="font-oswald font-bold text-ink text-sm" style="letter-spacing:0.16em; text-transform:uppercase;">
      Finalizing your subscription…
    </p>
    <p class="font-oswald text-ink-soft text-sm mt-2" style="letter-spacing:0.04em;">
      Hold tight — confirming your payment with the bank.
    </p>
  </div>
{:else if step === 'form'}
  <div class="mt-8">
    {#if error}
      <div class="mb-5 border-2 border-rust bg-cream-hi p-3 text-center">
        <p class="font-oswald font-bold text-rust text-sm" style="letter-spacing:0.04em;">
          {error}
        </p>
      </div>
    {/if}

    <!-- Box editor: what's in it, and a way to add or remove -->
    <section class="space-y-3 mb-8">
      <div class="flex items-center justify-between">
        <h2 class="font-slab uppercase text-ink leading-[0.95]" style="font-size: clamp(1.5rem, 3vw, 1.875rem); letter-spacing:-0.005em;">
          Your box
        </h2>
        {#if lines.length < 10}
          <button
            type="button"
            onclick={openPicker}
            class="font-oswald font-bold text-rust text-xs"
            style="letter-spacing:0.14em; text-transform:uppercase;"
          >+ Add another item</button>
        {/if}
      </div>

      <ul class="space-y-2">
        {#each lines as line, i (line.variant_id + line.plan_id)}
          <li class="border-2 border-ink bg-cream-hi p-3 flex items-center justify-between gap-3">
            <div class="min-w-0">
              <p class="font-oswald font-bold text-ink text-sm truncate" style="letter-spacing:0.02em;">
                {line.product_title}{line.variant_label ? ` — ${line.variant_label}` : ''}
              </p>
              <p class="font-oswald text-ink-soft text-xs" style="letter-spacing:0.04em;">
                {line.plan_name} · Qty {line.quantity} · {line.unit_price === null ? 'priced at payment' : formatCents(line.unit_price)}
              </p>
            </div>
            {#if lines.length > 1}
              <button
                type="button"
                onclick={() => removeLine(i)}
                class="font-oswald font-bold text-chrome-deep text-xs shrink-0 hover:text-rust"
                style="letter-spacing:0.1em; text-transform:uppercase;"
                aria-label={`Remove ${line.product_title}`}
              >Remove</button>
            {/if}
          </li>
        {/each}
      </ul>

      {#if pickerOpen}
        <div class="border-2 border-ink bg-cream-hi p-4 space-y-3">
          {#if pickerError}
            <p class="font-oswald text-rust text-sm" role="alert">{pickerError}</p>
          {/if}
          {#if catalogError}
            <p class="font-oswald text-rust text-sm">{catalogError}</p>
          {:else if !catalog}
            <p class="font-oswald text-ink-soft text-sm">Loading items…</p>
          {:else}
            <div>
              <label for="picker-variant" class={labelClasses} style={labelStyle}>Item</label>
              <select id="picker-variant" bind:value={pickerVariantId} class={inputClasses} style={inputStyle}>
                {#each catalog.variants as v (v.variant_id)}
                  <option value={v.variant_id}>{v.product_title}{v.variant_label ? ` — ${v.variant_label}` : ''} · {formatCents(v.base_price)}</option>
                {/each}
              </select>
            </div>
            <div>
              <label for="picker-plan" class={labelClasses} style={labelStyle}>Plan</label>
              <select id="picker-plan" bind:value={pickerPlanId} class={inputClasses} style={inputStyle}>
                {#each catalog.plans as p (p.plan_id)}
                  <option value={p.plan_id}>{p.name}{p.discount_pct > 0 ? ` (${p.discount_pct}% off)` : ''}</option>
                {/each}
              </select>
            </div>
            <div>
              <label for="picker-qty" class={labelClasses} style={labelStyle}>Quantity</label>
              <input id="picker-qty" type="number" min="1" max="10" bind:value={pickerQuantity} class={inputClasses} style={inputStyle} />
            </div>
            <div class="flex gap-3">
              <button
                type="button"
                onclick={addPickedItem}
                disabled={!pickerVariantId || !pickerPlanId}
                class="btn-stamp flex-1 bg-rust text-paper border-2 border-ink px-4 py-2.5 font-oswald font-bold text-xs disabled:opacity-60"
                style="letter-spacing:0.14em; text-transform:uppercase;"
              >Add to box</button>
              <button
                type="button"
                onclick={closePicker}
                class="font-oswald font-bold text-chrome-deep text-xs px-4"
                style="letter-spacing:0.14em; text-transform:uppercase;"
              >Cancel</button>
            </div>
          {/if}
        </div>
      {/if}
    </section>

    <form onsubmit={handleSubmit} class="space-y-8">
      <!-- Contact & shipping -->
      <section class="space-y-4">
        <h2
          class="font-slab uppercase text-ink leading-[0.95]"
          style="font-size: clamp(1.5rem, 3vw, 1.875rem); letter-spacing:-0.005em;"
        >
          Contact &amp; shipping
        </h2>

        <div>
          <label for="sub-email" class={labelClasses} style={labelStyle}>Email</label>
          <input
            id="sub-email"
            name="email"
            type="email"
            bind:value={email}
            onblur={handleAddressBlur}
            placeholder="you@example.com"
            required
            autocomplete="email"
            class={inputClasses}
            style={inputStyle}
          />
        </div>

        <div class="grid grid-cols-2 gap-4">
          <div>
            <label for="sub-firstName" class={labelClasses} style={labelStyle}>First name</label>
            <input
              id="sub-firstName"
              name="first-name"
              type="text"
              bind:value={firstName}
              onblur={handleAddressBlur}
              required
              autocomplete="given-name"
              class={inputClasses}
              style={inputStyle}
            />
          </div>
          <div>
            <label for="sub-lastName" class={labelClasses} style={labelStyle}>Last name</label>
            <input
              id="sub-lastName"
              name="last-name"
              type="text"
              bind:value={lastName}
              onblur={handleAddressBlur}
              required
              autocomplete="family-name"
              class={inputClasses}
              style={inputStyle}
            />
          </div>
        </div>

        <div>
          <label for="sub-line1" class={labelClasses} style={labelStyle}>Address</label>
          <input
            id="sub-line1"
            name="address-line1"
            type="text"
            bind:value={line1}
            onblur={handleAddressBlur}
            required
            autocomplete="shipping address-line1"
            class={inputClasses}
            style={inputStyle}
          />
        </div>

        <div>
          <label for="sub-line2" class={labelClasses} style={labelStyle}
            >Apt, suite, etc. (optional)</label
          >
          <input
            id="sub-line2"
            name="address-line2"
            type="text"
            bind:value={line2}
            onblur={handleAddressBlur}
            autocomplete="shipping address-line2"
            class={inputClasses}
            style={inputStyle}
          />
        </div>

        <div class="grid grid-cols-3 gap-4">
          <div>
            <label for="sub-city" class={labelClasses} style={labelStyle}>City</label>
            <input
              id="sub-city"
              name="city"
              type="text"
              bind:value={city}
              onblur={handleAddressBlur}
              required
              autocomplete="shipping address-level2"
              class={inputClasses}
              style={inputStyle}
            />
          </div>
          <div>
            <label for="sub-state" class={labelClasses} style={labelStyle}>State</label>
            <input
              id="sub-state"
              name="state"
              type="text"
              bind:value={addressState}
              onblur={handleAddressBlur}
              placeholder="WA"
              required
              autocomplete="shipping address-level1"
              class={inputClasses}
              style={inputStyle}
            />
          </div>
          <div>
            <label for="sub-postalCode" class={labelClasses} style={labelStyle}>ZIP code</label>
            <input
              id="sub-postalCode"
              name="postal-code"
              type="text"
              bind:value={postalCode}
              onblur={handleAddressBlur}
              placeholder="99336"
              required
              autocomplete="shipping postal-code"
              class={inputClasses}
              style={inputStyle}
            />
          </div>
        </div>

        <div>
          <label for="sub-country" class={labelClasses} style={labelStyle}>Country</label>
          <select
            id="sub-country"
            name="country"
            bind:value={country}
            onchange={handleAddressBlur}
            autocomplete="shipping country"
            class={inputClasses}
            style={inputStyle}
          >
            <option value="US">United States</option>
          </select>
        </div>
      </section>

      <!-- Payment -->
      <section class="space-y-4">
        <h2
          class="font-slab uppercase text-ink leading-[0.95]"
          style="font-size: clamp(1.5rem, 3vw, 1.875rem); letter-spacing:-0.005em;"
        >
          Payment
        </h2>

        {#if !stripeReady}
          <div class="border-2 border-ink bg-cream-hi p-6 text-center">
            <p
              class="font-oswald text-ink-soft text-sm"
              style="letter-spacing:0.04em;"
            >
              {#if formValid}
                Loading payment…
              {:else}
                Fill in your shipping details above to continue.
              {/if}
            </p>
          </div>
        {:else}
          {#if totals}
            <!-- Charge breakdown — what the card is actually charged today. -->
            <div class="border-2 border-ink bg-cream-hi p-4 sm:p-5 space-y-2">
              <div class="flex items-center justify-between font-oswald text-sm text-ink" style="letter-spacing:0.04em;">
                <span>Your box</span>
                <span class="font-special">{formatCents(totals.subtotal)}</span>
              </div>
              <div class="flex items-center justify-between font-oswald text-sm text-ink" style="letter-spacing:0.04em;">
                <span>Shipping</span>
                <span class="font-special">
                  {#if totals.shipping_total === 0}
                    {totals.shipping_label || 'Free'}
                  {:else}
                    {formatCents(totals.shipping_total)}
                  {/if}
                </span>
              </div>
              {#if totals.tax_total > 0}
                <div class="flex items-center justify-between font-oswald text-sm text-ink" style="letter-spacing:0.04em;">
                  <span>{totals.tax_label || 'Tax'}</span>
                  <span class="font-special">{formatCents(totals.tax_total)}</span>
                </div>
              {/if}
              <div class="flex items-center justify-between border-t-2 border-ink pt-2 font-oswald font-bold text-ink" style="letter-spacing:0.04em;">
                <span class="uppercase" style="letter-spacing:0.08em;">Charged today</span>
                <span class="font-special text-base">{formatCents(totals.amount)}</span>
              </div>
              <p class="font-oswald text-chrome-deep text-xs pt-1" style="letter-spacing:0.04em;">
                Auto-renews each delivery until you cancel. Manage it anytime from your account.
              </p>
            </div>
          {/if}
          <div
            id="stripe-subscribe-payment"
            class="border-2 border-ink bg-cream-hi p-4 sm:p-5"
          ></div>
        {/if}
      </section>

      <button
        type="submit"
        disabled={processing || !stripeReady}
        class="btn-stamp w-full inline-flex items-center justify-center gap-2 bg-rust text-paper border-2 border-ink px-6 py-4 font-oswald font-bold text-sm disabled:opacity-60 disabled:cursor-not-allowed"
        style="letter-spacing:0.16em; text-transform:uppercase;"
      >
        {#if processing}
          <svg class="size-4 animate-spin" fill="none" viewBox="0 0 24 24">
            <circle
              class="opacity-25"
              cx="12"
              cy="12"
              r="10"
              stroke="currentColor"
              stroke-width="3"
            ></circle>
            <path
              class="opacity-75"
              fill="currentColor"
              d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"
            ></path>
          </svg>
          Processing…
        {:else}
          {totals ? `Subscribe · ${formatCents(totals.amount)}` : 'Subscribe'}
          <svg
            class="size-4"
            fill="none"
            viewBox="0 0 24 24"
            stroke-width="2.5"
            stroke="currentColor"
            aria-hidden="true"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              d="M13.5 4.5 21 12m0 0-7.5 7.5M21 12H3"
            />
          </svg>
        {/if}
      </button>
    </form>
  </div>
{:else if confirmationStatus === 'processing'}
  <!-- Payment processing — the webhook activates the subscriptions server-side. -->
  <div class="mt-10 text-center">
    <div class="inline-flex items-center justify-center mb-6">
      <span
        class="relative inline-flex size-20 items-center justify-center bg-paper-warm border-2 border-ink"
        style="box-shadow: var(--shadow-stamp); transform: rotate(-4deg);"
      >
        <svg
          class="size-10 text-ink"
          fill="none"
          viewBox="0 0 24 24"
          stroke-width="2.5"
          stroke="currentColor"
          aria-hidden="true"
        >
          <path stroke-linecap="round" stroke-linejoin="round" d="M12 6v6h4.5m4.5 0a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z" />
        </svg>
        <span
          class="absolute -bottom-2 -right-3 inline-block font-oswald font-bold text-[10px] text-ink bg-candle px-2 py-0.5 border-2 border-ink"
          style="letter-spacing:0.16em; text-transform:uppercase; transform:rotate(-10deg);"
        >
          Pending
        </span>
      </span>
    </div>

    <p
      class="font-oswald text-chrome-deep text-xs font-semibold"
      style="letter-spacing:0.24em; text-transform:uppercase;"
    >
      Payment processing
    </p>
    <h2
      class="font-slab uppercase leading-[0.92] text-ink mt-3"
      style="font-size: clamp(2rem, 4vw, 2.5rem); letter-spacing:-0.005em;"
    >
      Order received.
    </h2>
    <p class="mt-5 font-oswald text-ink-soft text-base leading-relaxed max-w-md mx-auto">
      Your payment is still clearing. As soon as it does — usually within a few
      minutes — we'll activate
      {#if lines.length > 1}
        your subscriptions
      {:else}
        your <strong class="font-oswald font-bold text-ink">{lines[0]?.product_title}</strong> subscription
      {/if}
      and email your confirmation. No need to order again.
    </p>

    <a
      href="/catalog"
      class="btn-stamp inline-flex items-center gap-2 mt-8 bg-rust text-paper border-2 border-ink px-7 py-3.5 font-oswald font-bold text-sm"
      style="letter-spacing:0.14em; text-transform:uppercase;"
    >
      Keep shopping
      <svg
        class="size-4"
        fill="none"
        viewBox="0 0 24 24"
        stroke-width="2.5"
        stroke="currentColor"
        aria-hidden="true"
      >
        <path
          stroke-linecap="round"
          stroke-linejoin="round"
          d="M13.5 4.5 21 12m0 0-7.5 7.5M21 12H3"
        />
      </svg>
    </a>
  </div>
{:else}
  <!-- Confirmation — names every item the box started. -->
  <div class="mt-10 text-center">
    <div class="inline-flex items-center justify-center mb-6">
      <span
        class="relative inline-flex size-20 items-center justify-center bg-candle border-2 border-ink"
        style="box-shadow: var(--shadow-stamp); transform: rotate(-4deg);"
      >
        <svg
          class="size-10 text-ink"
          fill="none"
          viewBox="0 0 24 24"
          stroke-width="3"
          stroke="currentColor"
          aria-hidden="true"
        >
          <path stroke-linecap="round" stroke-linejoin="round" d="m4.5 12.75 6 6 9-13.5" />
        </svg>
        <span
          class="absolute -bottom-2 -right-3 inline-block font-oswald font-bold text-[10px] text-rust bg-paper px-2 py-0.5 border-2 border-rust"
          style="letter-spacing:0.16em; text-transform:uppercase; transform:rotate(-10deg); outline:2px solid var(--color-rust); outline-offset:2px;"
        >
          Active
        </span>
      </span>
    </div>

    <p
      class="font-oswald text-chrome-deep text-xs font-semibold"
      style="letter-spacing:0.24em; text-transform:uppercase;"
    >
      Subscription{lines.length > 1 ? 's' : ''} active
    </p>
    <h2
      class="font-slab uppercase leading-[0.92] text-ink mt-3"
      style="font-size: clamp(2rem, 4vw, 2.5rem); letter-spacing:-0.005em;"
    >
      You're on the
      <span
        class="font-script text-rust normal-case inline-block align-baseline"
        style="font-size:1.1em; letter-spacing:0;">list.</span
      >
    </h2>
    {#if lines.length > 1}
      <ul class="mt-5 max-w-md mx-auto text-left space-y-1">
        {#each lines as line (line.variant_id + line.plan_id)}
          <li class="font-oswald text-ink-soft text-sm">
            <strong class="font-oswald font-bold text-ink">{line.product_title}</strong> — {line.plan_name}
          </li>
        {/each}
      </ul>
      <p class="mt-4 font-oswald text-ink-soft text-base leading-relaxed max-w-md mx-auto">
        Confirmation email's on its way with the details.
      </p>
    {:else}
      <p class="mt-5 font-oswald text-ink-soft text-base leading-relaxed max-w-md mx-auto">
        Your <strong class="font-oswald font-bold text-ink">{lines[0]?.product_title}</strong> subscription is live.
        Confirmation email's on its way with the details.
      </p>
    {/if}

    <a
      href="/catalog"
      class="btn-stamp inline-flex items-center gap-2 mt-8 bg-rust text-paper border-2 border-ink px-7 py-3.5 font-oswald font-bold text-sm"
      style="letter-spacing:0.14em; text-transform:uppercase;"
    >
      Keep shopping
      <svg
        class="size-4"
        fill="none"
        viewBox="0 0 24 24"
        stroke-width="2.5"
        stroke="currentColor"
        aria-hidden="true"
      >
        <path
          stroke-linecap="round"
          stroke-linejoin="round"
          d="M13.5 4.5 21 12m0 0-7.5 7.5M21 12H3"
        />
      </svg>
    </a>
  </div>
{/if}
