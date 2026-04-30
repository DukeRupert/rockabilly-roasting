<script lang="ts">
  import { onMount, tick } from 'svelte';
  import type { Stripe, StripeElements } from '@stripe/stripe-js';
  import { getStripe, createElements } from '../lib/stripe';
  import {
    createPaymentIntent,
    confirmOrder,
    type CartResponse,
    type LocalFulfillmentMethod,
    type PaymentIntentResponse,
  } from '../lib/api';
  import { formatCents } from '../lib/format';

  interface Props {
    cart: CartResponse;
    stripeKey: string;
    customerId: string;
    addressId: string;
    eligibleLocalMethods: LocalFulfillmentMethod[];
    localPickupInstructions: string;
    localDeliveryDays: string;
    shippingMethod: LocalFulfillmentMethod | '';
    onShippingMethodChange: (m: LocalFulfillmentMethod) => void;
    totalsLoaded?: (pi: PaymentIntentResponse) => void;
    onBack: () => void;
  }

  let {
    cart,
    stripeKey,
    customerId,
    addressId,
    eligibleLocalMethods,
    localPickupInstructions,
    localDeliveryDays,
    shippingMethod,
    onShippingMethodChange,
    totalsLoaded,
    onBack,
  }: Props = $props();

  let resolvedTotal = $state<number | null>(null);
  let totalAmount = $derived(resolvedTotal ?? cart.subtotal);
  let showMethodRadio = $derived(eligibleLocalMethods.length > 1);

  let stripe = $state<Stripe | null>(null);
  let elements = $state<StripeElements | null>(null);
  let loading = $state(true);
  let processing = $state(false);
  let error = $state('');
  let clientSecret = $state('');

  // Track the method the current PI was created for. When the user toggles
  // the radio, we tear down the Stripe element and re-create the PI so the
  // pre-created order gets stamped with the new method. The previous order
  // stays in payment_status=awaiting and Stripe auto-cancels its PI within a
  // day — same lifecycle as a customer abandoning checkout.
  let initializedMethod = $state<LocalFulfillmentMethod | '' | null>(null);

  onMount(async () => {
    stripe = await getStripe(stripeKey);
    if (!stripe) {
      error = 'Failed to load payment system';
      loading = false;
      return;
    }
    await initializePayment();
  });

  $effect(() => {
    // Re-init when the user picks a different local fulfillment method, but
    // only after the first init completes — onMount handles the initial call.
    if (
      stripe &&
      initializedMethod !== null &&
      shippingMethod !== initializedMethod &&
      !processing
    ) {
      initializePayment();
    }
  });

  async function initializePayment() {
    if (!stripe) return;
    loading = true;
    error = '';
    try {
      const piResponse = await createPaymentIntent({
        cart_id: cart.cart_id,
        address_id: addressId,
        customer_id: customerId,
        shipping_method: shippingMethod || '',
      });

      clientSecret = piResponse.client_secret;
      resolvedTotal = piResponse.amount;
      initializedMethod = shippingMethod;
      totalsLoaded?.(piResponse);

      loading = false;
      await tick();

      // Stripe Elements can't swap clientSecret in place — clear the mount
      // point and create a fresh Elements instance every time.
      const mount = document.getElementById('stripe-payment-element');
      if (mount) mount.innerHTML = '';
      elements = createElements(stripe, clientSecret);
      const paymentElement = elements.create('payment');
      paymentElement.mount('#stripe-payment-element');
    } catch (e: any) {
      error = e.message || 'Failed to initialize payment';
      loading = false;
    }
  }

  async function handleSubmit(e: Event) {
    e.preventDefault();
    if (!stripe || !elements) return;

    processing = true;
    error = '';

    try {
      const { error: stripeError, paymentIntent } = await stripe.confirmPayment({
        elements,
        confirmParams: {
          return_url: window.location.origin + '/checkout',
        },
        redirect: 'if_required',
      });

      if (stripeError) {
        error = stripeError.message || 'Payment failed';
        processing = false;
        return;
      }

      if (!paymentIntent || paymentIntent.status !== 'succeeded') {
        error = 'Payment was not completed. Please try again.';
        processing = false;
        return;
      }

      const result = await confirmOrder({
        payment_intent_id: paymentIntent.id,
      });

      // Navigate to server-rendered confirmation page so it survives refresh.
      window.location.href = result.redirect;
    } catch (e: any) {
      error = e.message || 'Failed to complete order';
    } finally {
      processing = false;
    }
  }
</script>

<section aria-labelledby="payment-heading">
  <p
    class="font-oswald text-chrome-deep text-xs font-semibold"
    style="letter-spacing:0.24em; text-transform:uppercase;"
  >
    Step 2
  </p>
  <h2
    id="payment-heading"
    class="font-slab text-ink uppercase leading-[0.95] mt-2 mb-6"
    style="font-size: clamp(1.75rem, 3.5vw, 2.25rem); letter-spacing:-0.005em;"
  >
    Payment
  </h2>

  {#if error}
    <div class="mb-5 border-2 border-rust bg-cream-hi p-3 text-center">
      <p class="font-oswald font-bold text-rust text-sm" style="letter-spacing:0.04em;">
        {error}
      </p>
    </div>
  {/if}

  {#if showMethodRadio}
    <fieldset class="mb-6 border-2 border-ink bg-cream-hi p-4 sm:p-5">
      <legend
        class="px-2 font-oswald font-bold text-ink text-[11px]"
        style="letter-spacing:0.2em; text-transform:uppercase;"
      >
        Local fulfillment
      </legend>
      <div class="space-y-3">
        {#each eligibleLocalMethods as method}
          <label class="flex items-start gap-3 cursor-pointer">
            <input
              type="radio"
              name="shipping_method"
              value={method}
              checked={shippingMethod === method}
              onchange={() => onShippingMethodChange(method)}
              disabled={processing}
              class="mt-1 size-4 accent-rust"
            />
            <span class="flex-1">
              <span
                class="block font-oswald font-bold text-ink text-sm"
                style="letter-spacing:0.04em;"
              >
                {method === 'pickup' ? 'Free pickup at the shop' : 'Free local delivery'}
              </span>
              <span class="block font-oswald text-ink-soft text-xs mt-0.5">
                {#if method === 'pickup'}
                  {localPickupInstructions || "We'll email you when your order's ready."}
                {:else}
                  {localDeliveryDays
                    ? `Out for delivery on ${localDeliveryDays}.`
                    : "We'll email you when your order goes out for delivery."}
                {/if}
              </span>
            </span>
          </label>
        {/each}
      </div>
    </fieldset>
  {:else if eligibleLocalMethods.length === 1 && shippingMethod === 'pickup' && localPickupInstructions}
    <div class="mb-6 border-2 border-ink bg-cream-hi p-4 sm:p-5">
      <p
        class="font-oswald font-bold text-ink text-[11px] mb-1"
        style="letter-spacing:0.2em; text-transform:uppercase;"
      >
        Free pickup at the shop
      </p>
      <p class="font-oswald text-ink-soft text-sm">{localPickupInstructions}</p>
    </div>
  {/if}

  {#if loading}
    <div class="text-center py-8">
      <p class="font-oswald text-ink-soft text-sm" style="letter-spacing:0.04em;">
        Preparing payment…
      </p>
    </div>
  {:else}
    <form onsubmit={handleSubmit} class="space-y-6">
      <!-- Stripe Payment Element mounts here; theming handled in stripe.ts appearance config -->
      <div
        id="stripe-payment-element"
        class="border-2 border-ink bg-cream-hi p-4 sm:p-5"
      ></div>

      <div class="flex items-center justify-between gap-4 pt-2">
        <button
          type="button"
          onclick={onBack}
          disabled={processing}
          class="inline-flex items-center gap-1.5 font-oswald font-bold text-[11px] text-ink hover:text-rust transition-colors disabled:opacity-50"
          style="letter-spacing:0.2em; text-transform:uppercase;"
        >
          <svg
            class="size-3"
            fill="none"
            viewBox="0 0 24 24"
            stroke-width="2.5"
            stroke="currentColor"
            aria-hidden="true"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              d="M10.5 19.5 3 12m0 0 7.5-7.5M3 12h18"
            />
          </svg>
          Back
        </button>

        <button
          type="submit"
          disabled={processing}
          class="btn-stamp inline-flex items-center gap-2 bg-rust text-paper border-2 border-ink px-6 py-3.5 font-oswald font-bold text-sm disabled:opacity-60 disabled:cursor-not-allowed"
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
            Pay {formatCents(totalAmount)}
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
      </div>
    </form>
  {/if}
</section>
