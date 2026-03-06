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
    'w-full rounded-md border border-stone-300 px-3 py-2 text-sm text-stone-900 placeholder:text-stone-400 focus:border-hiri-teal focus:ring-1 focus:ring-hiri-teal focus:outline-none';
  const labelClasses = 'block text-sm font-medium text-stone-700 mb-1';

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
      <div class="mb-4 rounded-md bg-red-50 p-3 text-sm text-red-700">{error}</div>
    {/if}

    <form onsubmit={handleSubmit} class="space-y-6">
      <!-- Contact & Shipping -->
      <div class="space-y-4">
        <h2 class="text-xl font-semibold text-stone-900">Contact & shipping</h2>

        <div>
          <label for="sub-email" class={labelClasses}>Email</label>
          <input
            id="sub-email"
            type="email"
            bind:value={email}
            onblur={handleAddressBlur}
            placeholder="you@example.com"
            required
            class={inputClasses}
          />
        </div>

        <div class="grid grid-cols-2 gap-4">
          <div>
            <label for="sub-firstName" class={labelClasses}>First name</label>
            <input
              id="sub-firstName"
              type="text"
              bind:value={firstName}
              onblur={handleAddressBlur}
              required
              class={inputClasses}
            />
          </div>
          <div>
            <label for="sub-lastName" class={labelClasses}>Last name</label>
            <input
              id="sub-lastName"
              type="text"
              bind:value={lastName}
              onblur={handleAddressBlur}
              required
              class={inputClasses}
            />
          </div>
        </div>

        <div>
          <label for="sub-line1" class={labelClasses}>Address</label>
          <input
            id="sub-line1"
            type="text"
            bind:value={line1}
            onblur={handleAddressBlur}
            required
            class={inputClasses}
          />
        </div>

        <div>
          <label for="sub-line2" class={labelClasses}>Apartment, suite, etc. (optional)</label>
          <input
            id="sub-line2"
            type="text"
            bind:value={line2}
            onblur={handleAddressBlur}
            class={inputClasses}
          />
        </div>

        <div class="grid grid-cols-3 gap-4">
          <div>
            <label for="sub-city" class={labelClasses}>City</label>
            <input
              id="sub-city"
              type="text"
              bind:value={city}
              onblur={handleAddressBlur}
              required
              class={inputClasses}
            />
          </div>
          <div>
            <label for="sub-state" class={labelClasses}>State</label>
            <input
              id="sub-state"
              type="text"
              bind:value={addressState}
              onblur={handleAddressBlur}
              placeholder="CA"
              required
              class={inputClasses}
            />
          </div>
          <div>
            <label for="sub-postalCode" class={labelClasses}>ZIP code</label>
            <input
              id="sub-postalCode"
              type="text"
              bind:value={postalCode}
              onblur={handleAddressBlur}
              placeholder="90210"
              required
              class={inputClasses}
            />
          </div>
        </div>

        <div>
          <label for="sub-country" class={labelClasses}>Country</label>
          <select
            id="sub-country"
            bind:value={country}
            onchange={handleAddressBlur}
            class={inputClasses}
          >
            <option value="US">United States</option>
          </select>
        </div>
      </div>

      <!-- Payment -->
      <div class="space-y-4">
        <h2 class="text-xl font-semibold text-stone-900">Payment</h2>

        {#if !stripeReady}
          <div class="rounded-md border border-stone-200 bg-stone-50 p-6 text-center">
            <p class="text-sm text-stone-500">
              {#if formValid}
                Loading payment...
              {:else}
                Fill in your shipping details above to continue.
              {/if}
            </p>
          </div>
        {:else}
          <div id="stripe-subscribe-payment"></div>
        {/if}
      </div>

      <button
        type="submit"
        disabled={processing || !stripeReady}
        class="w-full rounded-md bg-hiri-teal px-6 py-3 text-base font-semibold text-white shadow-sm hover:bg-hiri-teal-dark focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-hiri-teal disabled:opacity-50 disabled:cursor-not-allowed"
      >
        {#if processing}
          Processing...
        {:else}
          Subscribe &mdash; {formatCents(price)}
        {/if}
      </button>
    </form>
  </div>
{:else}
  <!-- Confirmation -->
  <div class="mt-8 text-center py-12">
    <div class="mx-auto flex h-16 w-16 items-center justify-center rounded-full bg-green-100 mb-6">
      <svg
        class="h-8 w-8 text-green-600"
        fill="none"
        viewBox="0 0 24 24"
        stroke-width="2"
        stroke="currentColor"
      >
        <path stroke-linecap="round" stroke-linejoin="round" d="M4.5 12.75l6 6 9-13.5" />
      </svg>
    </div>

    <h2 class="text-2xl font-bold text-stone-900 mb-2">You're subscribed!</h2>
    <p class="text-stone-600 mb-1">
      Your <span class="font-medium">{planName}</span> subscription is now active.
    </p>
    <p class="text-sm text-stone-500 mt-4 mb-8">
      We'll send a confirmation email with your subscription details shortly.
    </p>

    <a
      href="/catalog"
      class="inline-block rounded-md bg-hiri-teal px-6 py-3 text-base font-semibold text-white shadow-sm hover:bg-hiri-teal-dark"
    >
      Continue shopping
    </a>
  </div>
{/if}
