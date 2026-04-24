<script lang="ts">
  import { submitAddress, type CartResponse } from '../lib/api';

  interface Props {
    cart: CartResponse;
    onComplete: (result: { customerId: string; addressId: string }) => void;
  }

  let { cart, onComplete }: Props = $props();

  let email = $state('');
  let firstName = $state('');
  let lastName = $state('');
  let line1 = $state('');
  let line2 = $state('');
  let city = $state('');
  let state = $state('');
  let postalCode = $state('');
  let country = $state('US');

  let submitting = $state(false);
  let errors = $state<Record<string, string>>({});
  let generalError = $state('');

  async function handleSubmit(e: Event) {
    e.preventDefault();
    submitting = true;
    errors = {};
    generalError = '';

    try {
      const result = await submitAddress({
        email,
        first_name: firstName,
        last_name: lastName,
        line1,
        line2: line2 || undefined,
        city,
        state,
        postal_code: postalCode,
        country,
      });

      onComplete({
        customerId: result.customer_id,
        addressId: result.address_id,
      });
    } catch (err: any) {
      if (err.errors) {
        errors = err.errors;
      } else {
        generalError = err.message || 'Failed to save address';
      }
    } finally {
      submitting = false;
    }
  }

  const inputClasses =
    'paper-input w-full border-2 border-ink bg-cream-hi px-3.5 py-2.5 font-oswald text-sm text-ink placeholder:text-chrome-deep focus:outline-none';
  const labelClasses =
    'font-oswald font-bold text-ink text-[11px] mb-2 block';
  const labelStyle = 'letter-spacing:0.2em; text-transform:uppercase;';
  const inputStyle = 'letter-spacing:0.04em;';
  const errorClasses = 'mt-1.5 font-oswald font-bold text-rust text-xs';
</script>

<section aria-labelledby="information-heading">
  <p
    class="font-oswald text-chrome-deep text-xs font-semibold"
    style="letter-spacing:0.24em; text-transform:uppercase;"
  >
    Step 1
  </p>
  <h2
    id="information-heading"
    class="font-slab text-ink uppercase leading-[0.95] mt-2 mb-6"
    style="font-size: clamp(1.75rem, 3.5vw, 2.25rem); letter-spacing:-0.005em;"
  >
    Contact &amp; shipping
  </h2>

  {#if generalError}
    <div class="mb-5 border-2 border-rust bg-cream-hi p-3 text-center">
      <p class="font-oswald font-bold text-rust text-sm" style="letter-spacing:0.04em;">
        {generalError}
      </p>
    </div>
  {/if}

  <form onsubmit={handleSubmit} class="space-y-5">
    <!-- Email -->
    <div>
      <label for="email" class={labelClasses} style={labelStyle}>Email</label>
      <input
        id="email"
        type="email"
        bind:value={email}
        placeholder="you@example.com"
        required
        class={inputClasses}
        style={inputStyle}
      />
      {#if errors.email}<p class={errorClasses}>{errors.email}</p>{/if}
    </div>

    <!-- Name -->
    <div class="grid grid-cols-2 gap-4">
      <div>
        <label for="firstName" class={labelClasses} style={labelStyle}>First name</label>
        <input
          id="firstName"
          type="text"
          bind:value={firstName}
          required
          class={inputClasses}
          style={inputStyle}
        />
        {#if errors.first_name}<p class={errorClasses}>{errors.first_name}</p>{/if}
      </div>
      <div>
        <label for="lastName" class={labelClasses} style={labelStyle}>Last name</label>
        <input
          id="lastName"
          type="text"
          bind:value={lastName}
          required
          class={inputClasses}
          style={inputStyle}
        />
        {#if errors.last_name}<p class={errorClasses}>{errors.last_name}</p>{/if}
      </div>
    </div>

    <!-- Address -->
    <div>
      <label for="line1" class={labelClasses} style={labelStyle}>Address</label>
      <input
        id="line1"
        type="text"
        bind:value={line1}
        required
        class={inputClasses}
        style={inputStyle}
      />
      {#if errors.line1}<p class={errorClasses}>{errors.line1}</p>{/if}
    </div>

    <div>
      <label for="line2" class={labelClasses} style={labelStyle}
        >Apt, suite, etc. (optional)</label
      >
      <input
        id="line2"
        type="text"
        bind:value={line2}
        class={inputClasses}
        style={inputStyle}
      />
    </div>

    <div class="grid grid-cols-3 gap-4">
      <div>
        <label for="city" class={labelClasses} style={labelStyle}>City</label>
        <input
          id="city"
          type="text"
          bind:value={city}
          required
          class={inputClasses}
          style={inputStyle}
        />
        {#if errors.city}<p class={errorClasses}>{errors.city}</p>{/if}
      </div>
      <div>
        <label for="state" class={labelClasses} style={labelStyle}>State</label>
        <input
          id="state"
          type="text"
          bind:value={state}
          placeholder="WA"
          required
          class={inputClasses}
          style={inputStyle}
        />
        {#if errors.state}<p class={errorClasses}>{errors.state}</p>{/if}
      </div>
      <div>
        <label for="postalCode" class={labelClasses} style={labelStyle}>ZIP code</label>
        <input
          id="postalCode"
          type="text"
          bind:value={postalCode}
          placeholder="99336"
          required
          class={inputClasses}
          style={inputStyle}
        />
        {#if errors.postal_code}<p class={errorClasses}>{errors.postal_code}</p>{/if}
      </div>
    </div>

    <div>
      <label for="country" class={labelClasses} style={labelStyle}>Country</label>
      <select
        id="country"
        bind:value={country}
        class={inputClasses}
        style={inputStyle}
      >
        <option value="US">United States</option>
      </select>
    </div>

    <div class="pt-2">
      <button
        type="submit"
        disabled={submitting}
        class="btn-stamp w-full inline-flex items-center justify-center gap-2 bg-rust text-paper border-2 border-ink px-6 py-4 font-oswald font-bold text-sm disabled:opacity-60 disabled:cursor-not-allowed"
        style="letter-spacing:0.16em; text-transform:uppercase;"
      >
        {#if submitting}
          <svg class="size-4 animate-spin" fill="none" viewBox="0 0 24 24">
            <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="3"
            ></circle>
            <path
              class="opacity-75"
              fill="currentColor"
              d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"
            ></path>
          </svg>
          Saving…
        {:else}
          Continue to payment
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
</section>
