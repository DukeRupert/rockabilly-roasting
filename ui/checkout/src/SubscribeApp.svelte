<script lang="ts">
  import { onMount, tick } from 'svelte';
  import type { Stripe, StripeElements } from '@stripe/stripe-js';
  import { getStripe, createElements } from './lib/stripe';
  import {
    createSubscribePaymentIntent,
    confirmSubscription,
  } from './lib/subscribe-api';
  import { formatCents } from './lib/format';

  interface Props {
    planId: string;
    variantId: string;
    quantity: number;
    planName: string;
    price: number;
    interval: string;
    stripeKey: string;
  }

  let { planId, variantId, quantity, planName, price, interval, stripeKey }: Props = $props();

  type Step = 'form' | 'confirmation';

  let step = $state<Step>('form');
  let subscriptionId = $state('');

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
    if (formValid && !stripeInitialized) {
      stripeInitialized = true;
      initStripe();
    }
  });

  async function initStripe() {
    try {
      stripe = await getStripe(stripeKey);
      if (!stripe) {
        error = 'Failed to load payment system';
        return;
      }

      const piResponse = await createSubscribePaymentIntent({
        plan_id: planId,
        variant_id: variantId,
        quantity,
        email,
        first_name: firstName,
        last_name: lastName,
        line1,
        line2: line2 || undefined,
        city,
        state: addressState,
        postal_code: postalCode,
        country,
      });

      clientSecret = piResponse.client_secret;
      stripeReady = true;
      await tick();

      elements = createElements(stripe, clientSecret);
      const paymentElement = elements.create('payment');
      paymentElement.mount('#stripe-subscribe-payment');
    } catch (e: any) {
      error = e.message || 'Failed to initialize payment';
      stripeInitialized = false;
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

      if (!paymentIntent || paymentIntent.status !== 'succeeded') {
        error = 'Payment was not completed. Please try again.';
        processing = false;
        return;
      }

      const result = await confirmSubscription({
        plan_id: planId,
        variant_id: variantId,
        quantity,
        email,
        first_name: firstName,
        last_name: lastName,
        line1,
        line2: line2 || undefined,
        city,
        state: addressState,
        postal_code: postalCode,
        country,
        payment_intent_id: paymentIntent.id,
      });

      subscriptionId = result.subscription_id;
      step = 'confirmation';
    } catch (e: any) {
      error = e.message || 'Failed to complete subscription';
    } finally {
      processing = false;
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
      // Reset Stripe to get a new PI with updated address
      stripeReady = false;
      stripeInitialized = false;
      clientSecret = '';
      elements = null;
      stripeInitialized = true;
      initStripe();
    }
  }
</script>

{#if step === 'form'}
  <div class="mt-8">
    {#if error}
      <div class="mb-5 border-2 border-rust bg-cream-hi p-3 text-center">
        <p class="font-oswald font-bold text-rust text-sm" style="letter-spacing:0.04em;">
          {error}
        </p>
      </div>
    {/if}

    <form onsubmit={handleSubmit} class="space-y-8">
      <!-- Contact & shipping -->
      <section class="space-y-4">
        <h2
          class="font-slab text-ink uppercase leading-[0.95]"
          style="font-size: clamp(1.5rem, 3vw, 1.875rem); letter-spacing:-0.005em;"
        >
          Contact &amp; shipping
        </h2>

        <div>
          <label for="sub-email" class={labelClasses} style={labelStyle}>Email</label>
          <input
            id="sub-email"
            type="email"
            bind:value={email}
            onblur={handleAddressBlur}
            placeholder="you@example.com"
            required
            class={inputClasses}
            style={inputStyle}
          />
        </div>

        <div class="grid grid-cols-2 gap-4">
          <div>
            <label for="sub-firstName" class={labelClasses} style={labelStyle}>First name</label>
            <input
              id="sub-firstName"
              type="text"
              bind:value={firstName}
              onblur={handleAddressBlur}
              required
              class={inputClasses}
              style={inputStyle}
            />
          </div>
          <div>
            <label for="sub-lastName" class={labelClasses} style={labelStyle}>Last name</label>
            <input
              id="sub-lastName"
              type="text"
              bind:value={lastName}
              onblur={handleAddressBlur}
              required
              class={inputClasses}
              style={inputStyle}
            />
          </div>
        </div>

        <div>
          <label for="sub-line1" class={labelClasses} style={labelStyle}>Address</label>
          <input
            id="sub-line1"
            type="text"
            bind:value={line1}
            onblur={handleAddressBlur}
            required
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
            type="text"
            bind:value={line2}
            onblur={handleAddressBlur}
            class={inputClasses}
            style={inputStyle}
          />
        </div>

        <div class="grid grid-cols-3 gap-4">
          <div>
            <label for="sub-city" class={labelClasses} style={labelStyle}>City</label>
            <input
              id="sub-city"
              type="text"
              bind:value={city}
              onblur={handleAddressBlur}
              required
              class={inputClasses}
              style={inputStyle}
            />
          </div>
          <div>
            <label for="sub-state" class={labelClasses} style={labelStyle}>State</label>
            <input
              id="sub-state"
              type="text"
              bind:value={addressState}
              onblur={handleAddressBlur}
              placeholder="WA"
              required
              class={inputClasses}
              style={inputStyle}
            />
          </div>
          <div>
            <label for="sub-postalCode" class={labelClasses} style={labelStyle}>ZIP code</label>
            <input
              id="sub-postalCode"
              type="text"
              bind:value={postalCode}
              onblur={handleAddressBlur}
              placeholder="99336"
              required
              class={inputClasses}
              style={inputStyle}
            />
          </div>
        </div>

        <div>
          <label for="sub-country" class={labelClasses} style={labelStyle}>Country</label>
          <select
            id="sub-country"
            bind:value={country}
            onchange={handleAddressBlur}
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
          class="font-slab text-ink uppercase leading-[0.95]"
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
          Subscribe · {formatCents(price)}
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
{:else}
  <!-- Confirmation -->
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
      Subscription active
    </p>
    <h2
      class="font-slab text-ink uppercase leading-[0.92] mt-3"
      style="font-size: clamp(2rem, 4vw, 2.5rem); letter-spacing:-0.005em;"
    >
      You're on the
      <span
        class="font-script text-rust normal-case inline-block align-baseline"
        style="font-size:1.1em; letter-spacing:0;">list.</span
      >
    </h2>
    <p class="mt-5 font-oswald text-ink-soft text-base leading-relaxed max-w-md mx-auto">
      Your <strong class="font-oswald font-bold text-ink">{planName}</strong> subscription is live.
      Confirmation email's on its way with the details.
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
{/if}
