<script lang="ts">
  import {
    confirmOrder,
    getCart,
    type AddressResponse,
    type CartResponse,
    type LocalFulfillmentMethod,
    type PaymentIntentResponse,
  } from './lib/api';
  import Information from './steps/Information.svelte';
  import Payment from './steps/Payment.svelte';
  import { formatCents } from './lib/format';

  interface Props {
    cartId: string;
    stripeKey: string;
  }

  let { cartId, stripeKey }: Props = $props();

  type Step = 'information' | 'payment';

  let step = $state<Step>('information');
  let cart = $state<CartResponse | null>(null);
  let loading = $state(true);
  let error = $state('');
  // True while we're handling a Stripe redirect-back (Klarna and other async
  // methods). Suppresses the steps UI and shows a finalizing state instead.
  let finalizing = $state(false);

  // Checkout state carried between steps
  let customerId = $state('');
  let addressId = $state('');
  let eligibleLocalMethods = $state<LocalFulfillmentMethod[]>([]);
  let localPickupInstructions = $state('');
  let localDeliveryDays = $state('');
  let preferredLocalFulfillment = $state<LocalFulfillmentMethod | ''>('');
  let chosenShippingMethod = $state<LocalFulfillmentMethod | ''>('');
  let totals = $state<PaymentIntentResponse | null>(null);

  const steps: { key: Step; label: string }[] = [
    { key: 'information', label: 'Information' },
    { key: 'payment', label: 'Payment' },
  ];

  let currentStepIndex = $derived(steps.findIndex((s) => s.key === step));

  $effect(() => {
    handleRedirectBack();
  });

  // handleRedirectBack runs once on mount. If the URL carries Stripe's
  // redirect-back query params (payment_intent + redirect_status) we skip
  // the cart/steps flow entirely and finalize the order against the
  // already-pre-created order row. The server-side handleCheckoutConfirm
  // accepts both `succeeded` and `processing` PI statuses; for `processing`
  // (typical Klarna), the order stays in awaiting+pending and the
  // payment_intent.succeeded webhook will finalize it asynchronously.
  async function handleRedirectBack() {
    const params = new URLSearchParams(window.location.search);
    const paymentIntentId = params.get('payment_intent');
    const redirectStatus = params.get('redirect_status');

    if (!paymentIntentId) {
      // Normal flow — no redirect-back to handle. Load the cart.
      loadCart();
      return;
    }

    if (redirectStatus === 'failed') {
      // Klarna (or another redirect-based method) declined. Drop the query
      // params so a refresh doesn't re-enter this branch, then fall through
      // to the normal cart/steps flow. The server-side webhook will move
      // the linked order to payment_status=failed; the customer can retry.
      window.history.replaceState({}, '', '/checkout');
      error = 'Payment was not completed. You can choose another method below.';
      loadCart();
      return;
    }

    // succeeded or processing — drive the order to confirmation.
    finalizing = true;
    loading = false;
    try {
      const result = await confirmOrder({ payment_intent_id: paymentIntentId });
      window.location.href = result.redirect;
    } catch (e: any) {
      error = e.message || 'Failed to finalize order';
      finalizing = false;
      // Drop the query params so the customer can try again from a clean slate.
      window.history.replaceState({}, '', '/checkout');
      loadCart();
    }
  }

  async function loadCart() {
    try {
      loading = true;
      error = '';
      cart = await getCart();
      if (!cart.items.length) {
        error = 'Your cart is empty.';
      }
    } catch (e: any) {
      error = e.message || 'Failed to load cart';
    } finally {
      loading = false;
    }
  }

  function handleAddressComplete(e: AddressResponse) {
    customerId = e.customer_id;
    addressId = e.address_id;
    eligibleLocalMethods = e.eligible_local_methods ?? [];
    localPickupInstructions = e.local_pickup_instructions ?? '';
    localDeliveryDays = e.local_delivery_days ?? '';
    preferredLocalFulfillment = e.preferred_local_fulfillment ?? '';
    // Default the radio: saved preference if it's still eligible, otherwise
    // the first option. Empty string when there are zero or one option (one
    // option still gets stamped server-side; UI just doesn't ask).
    if (eligibleLocalMethods.length > 1) {
      chosenShippingMethod =
        preferredLocalFulfillment && eligibleLocalMethods.includes(preferredLocalFulfillment)
          ? preferredLocalFulfillment
          : eligibleLocalMethods[0];
    } else {
      chosenShippingMethod = '';
    }
    totals = null;
    step = 'payment';
  }

  function handleShippingMethodChange(method: LocalFulfillmentMethod) {
    chosenShippingMethod = method;
    // Force Payment to recreate the PI with the new method.
    totals = null;
  }

  function handleTotalsLoaded(pi: PaymentIntentResponse) {
    totals = pi;
  }
</script>

<!-- Full-bleed paper surface -->
<section class="-mx-4 sm:-mx-6 lg:-mx-8 relative bg-paper min-h-[calc(100vh-4rem)]">
  <!-- Paper grain overlay -->
  <div
    class="absolute inset-0 opacity-[0.05] pointer-events-none"
    style="background-image: radial-gradient(circle, rgba(14,13,12,0.6) 1px, transparent 1px); background-size: 3px 3px;"
  ></div>
  <div class="relative mx-auto max-w-6xl px-6 sm:px-10 lg:px-14 py-10 sm:py-14">
    <!-- Page heading -->
    <div class="mb-8 sm:mb-10">
      <p
        class="font-oswald text-chrome-deep text-xs font-semibold"
        style="letter-spacing:0.24em; text-transform:uppercase;"
      >
        Checkout
      </p>
      <h1
        class="font-slab text-ink uppercase leading-[0.92] mt-2"
        style="font-size: clamp(2.25rem, 5vw, 3.5rem); letter-spacing:-0.005em;"
      >
        Seal the
        <span
          class="font-script text-rust normal-case inline-block align-baseline"
          style="font-size:1.1em; letter-spacing:0;">deal.</span
        >
      </h1>
    </div>

    <!-- Step indicator -->
    <nav class="mb-10" aria-label="Checkout progress">
      <ol class="flex items-center gap-3 sm:gap-5">
        {#each steps as s, i}
          {#if i > 0}
            <li class="text-chrome-deep" aria-hidden="true">
              <svg
                class="size-4"
                fill="none"
                viewBox="0 0 24 24"
                stroke-width="2.5"
                stroke="currentColor"
              >
                <path stroke-linecap="round" stroke-linejoin="round" d="M8.25 4.5l7.5 7.5-7.5 7.5" />
              </svg>
            </li>
          {/if}
          <li class="flex items-center gap-2">
            {#if i < currentStepIndex}
              <!-- Completed -->
              <span
                class="inline-flex size-6 items-center justify-center border-2 border-ink bg-candle text-ink"
                aria-hidden="true"
              >
                <svg
                  class="size-3.5"
                  fill="none"
                  viewBox="0 0 24 24"
                  stroke-width="3"
                  stroke="currentColor"
                >
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    d="m4.5 12.75 6 6 9-13.5"
                  />
                </svg>
              </span>
              <span
                class="font-oswald font-bold text-ink text-[11px]"
                style="letter-spacing:0.2em; text-transform:uppercase;">{s.label}</span
              >
            {:else if i === currentStepIndex}
              <!-- Current -->
              <span
                class="inline-flex size-6 items-center justify-center border-2 border-ink bg-rust text-paper font-special text-xs"
                aria-current="step">{i + 1}</span
              >
              <span
                class="font-oswald font-bold text-rust text-[11px]"
                style="letter-spacing:0.2em; text-transform:uppercase;">{s.label}</span
              >
            {:else}
              <!-- Pending -->
              <span
                class="inline-flex size-6 items-center justify-center border-2 border-chrome-deep/40 bg-cream-hi text-chrome-deep font-special text-xs"
                aria-hidden="true">{i + 1}</span
              >
              <span
                class="font-oswald font-bold text-chrome-deep text-[11px]"
                style="letter-spacing:0.2em; text-transform:uppercase;">{s.label}</span
              >
            {/if}
          </li>
        {/each}
      </ol>
    </nav>

    {#if finalizing}
      <div class="border-2 border-ink bg-cream-hi py-16 px-6 text-center shadow-stamp">
        <p
          class="font-oswald font-bold text-ink text-base"
          style="letter-spacing:0.18em; text-transform:uppercase;"
        >
          Finalizing your order…
        </p>
        <p class="font-oswald text-ink-soft text-sm mt-3" style="letter-spacing:0.04em;">
          Don't close this window.
        </p>
      </div>
    {:else if loading}
      <div class="text-center py-16">
        <p class="font-oswald text-ink-soft text-sm" style="letter-spacing:0.04em;">
          Loading checkout…
        </p>
      </div>
    {:else if error && !cart?.items.length}
      <div class="border-2 border-ink bg-cream-hi py-16 px-6 text-center shadow-stamp">
        <p class="font-oswald text-ink text-base" style="letter-spacing:0.04em;">{error}</p>
        <a
          href="/cart"
          class="btn-stamp inline-flex items-center gap-2 mt-6 bg-rust text-paper border-2 border-ink px-6 py-3 font-oswald font-bold text-sm"
          style="letter-spacing:0.14em; text-transform:uppercase;"
        >
          <svg
            class="size-4"
            fill="none"
            viewBox="0 0 24 24"
            stroke-width="2.5"
            stroke="currentColor"
            aria-hidden="true"
          >
            <path stroke-linecap="round" stroke-linejoin="round" d="M10.5 19.5 3 12m0 0 7.5-7.5M3 12h18" />
          </svg>
          Return to cart
        </a>
      </div>
    {:else if cart}
      <div class="lg:grid lg:grid-cols-5 lg:gap-x-10">
        <!-- Main content -->
        <div class="lg:col-span-3">
          {#if step === 'information'}
            <Information {cart} onComplete={handleAddressComplete} />
          {:else if step === 'payment'}
            <Payment
              {cart}
              {stripeKey}
              {customerId}
              {addressId}
              {eligibleLocalMethods}
              {localPickupInstructions}
              {localDeliveryDays}
              shippingMethod={chosenShippingMethod}
              onShippingMethodChange={handleShippingMethodChange}
              totalsLoaded={handleTotalsLoaded}
              onBack={() => (step = 'information')}
            />
          {/if}
        </div>

        <!-- Order summary sidebar -->
        <aside class="mt-10 lg:mt-0 lg:col-span-2">
          <div class="border-2 border-ink bg-cream-hi shadow-stamp p-6 sm:p-7 lg:sticky lg:top-24">
            <p
              class="font-oswald font-bold text-candle text-[11px] mb-4 pb-2 border-b-2 border-ink"
              style="letter-spacing:0.24em; text-transform:uppercase;"
            >
              Order summary
            </p>
            <ul class="divide-y-2 divide-ink/30">
              {#each cart.items as item}
                <li class="py-3 flex justify-between gap-3">
                  <div class="min-w-0 flex-1">
                    <p
                      class="font-slab text-ink uppercase leading-[1.0] text-sm"
                      style="letter-spacing:-0.005em;"
                    >
                      {item.product_title}
                    </p>
                    <p class="font-special text-chrome-deep text-xs mt-1">
                      {item.sku} · qty {item.quantity}
                    </p>
                  </div>
                  <p class="font-special text-ink text-sm shrink-0">
                    {formatCents(item.line_total)}
                  </p>
                </li>
              {/each}
            </ul>
            <dl class="mt-4 pt-4 border-t-2 border-ink space-y-2">
              <div class="flex items-baseline justify-between">
                <dt class="font-oswald text-ink-soft text-sm" style="letter-spacing:0.04em;">
                  Subtotal
                </dt>
                <dd class="font-special text-ink text-base">{formatCents(cart.subtotal)}</dd>
              </div>
              {#if totals && totals.discount_total > 0}
                <div class="flex items-baseline justify-between">
                  <dt class="font-oswald text-rust text-sm" style="letter-spacing:0.04em;">
                    {totals.discount_name || 'Discount'}
                  </dt>
                  <dd class="font-special text-rust text-sm">
                    −{formatCents(totals.discount_total)}
                  </dd>
                </div>
              {/if}
              <div class="flex items-baseline justify-between">
                <dt class="font-oswald text-chrome-deep text-sm" style="letter-spacing:0.04em;">
                  Shipping
                </dt>
                <dd class="font-special text-chrome-deep text-sm">
                  {#if !totals}
                    Calculated next step
                  {:else if totals.shipping_total === 0}
                    {totals.shipping_label || 'Free'}
                  {:else}
                    {formatCents(totals.shipping_total)}
                  {/if}
                </dd>
              </div>
              {#if totals && totals.tax_total > 0}
                <div class="flex items-baseline justify-between">
                  <dt class="font-oswald text-chrome-deep text-sm" style="letter-spacing:0.04em;">
                    {totals.tax_label || 'Tax'}
                  </dt>
                  <dd class="font-special text-chrome-deep text-sm">
                    {formatCents(totals.tax_total)}
                  </dd>
                </div>
              {/if}
            </dl>
            <div
              class="mt-4 pt-3 border-t-2 border-ink flex items-baseline justify-between"
            >
              <span
                class="font-slab text-ink text-lg uppercase"
                style="letter-spacing:0.02em;">Total</span
              >
              <span class="font-special text-ink text-xl">
                {formatCents(totals ? totals.amount : cart.subtotal)}
              </span>
            </div>
          </div>
        </aside>
      </div>
    {/if}
  </div>
</section>
