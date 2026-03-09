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
    'w-full rounded-sm border border-rr-border bg-rr-bg px-3 py-2 text-sm font-body text-rr-heading placeholder:text-rr-border focus:border-rr-amber focus:ring-1 focus:ring-rr-amber focus:outline-none';
  const labelClasses = 'label-font text-rr-muted mb-1 block';
  const errorClasses = 'mt-1 text-sm text-rr-red';
</script>

<div>
  <h2 class="font-display text-2xl tracking-widest text-rr-heading mb-6">CONTACT &amp; SHIPPING</h2>

  {#if generalError}
    <div class="mb-4 rounded-sm bg-rr-red/10 p-3 text-sm text-rr-red-lt">{generalError}</div>
  {/if}

  <form onsubmit={handleSubmit} class="space-y-4">
    <!-- Email -->
    <div>
      <label for="email" class={labelClasses}>Email</label>
      <input
        id="email"
        type="email"
        bind:value={email}
        placeholder="you@example.com"
        required
        class={inputClasses}
      />
      {#if errors.email}<p class={errorClasses}>{errors.email}</p>{/if}
    </div>

    <!-- Name -->
    <div class="grid grid-cols-2 gap-4">
      <div>
        <label for="firstName" class={labelClasses}>First name</label>
        <input id="firstName" type="text" bind:value={firstName} required class={inputClasses} />
        {#if errors.first_name}<p class={errorClasses}>{errors.first_name}</p>{/if}
      </div>
      <div>
        <label for="lastName" class={labelClasses}>Last name</label>
        <input id="lastName" type="text" bind:value={lastName} required class={inputClasses} />
        {#if errors.last_name}<p class={errorClasses}>{errors.last_name}</p>{/if}
      </div>
    </div>

    <!-- Address -->
    <div>
      <label for="line1" class={labelClasses}>Address</label>
      <input id="line1" type="text" bind:value={line1} required class={inputClasses} />
      {#if errors.line1}<p class={errorClasses}>{errors.line1}</p>{/if}
    </div>

    <div>
      <label for="line2" class={labelClasses}>Apartment, suite, etc. (optional)</label>
      <input id="line2" type="text" bind:value={line2} class={inputClasses} />
    </div>

    <div class="grid grid-cols-3 gap-4">
      <div>
        <label for="city" class={labelClasses}>City</label>
        <input id="city" type="text" bind:value={city} required class={inputClasses} />
        {#if errors.city}<p class={errorClasses}>{errors.city}</p>{/if}
      </div>
      <div>
        <label for="state" class={labelClasses}>State</label>
        <input
          id="state"
          type="text"
          bind:value={state}
          placeholder="CA"
          required
          class={inputClasses}
        />
        {#if errors.state}<p class={errorClasses}>{errors.state}</p>{/if}
      </div>
      <div>
        <label for="postalCode" class={labelClasses}>ZIP code</label>
        <input
          id="postalCode"
          type="text"
          bind:value={postalCode}
          placeholder="90210"
          required
          class={inputClasses}
        />
        {#if errors.postal_code}<p class={errorClasses}>{errors.postal_code}</p>{/if}
      </div>
    </div>

    <div>
      <label for="country" class={labelClasses}>Country</label>
      <select id="country" bind:value={country} class={inputClasses}>
        <option value="US">United States</option>
      </select>
    </div>

    <button
      type="submit"
      disabled={submitting}
      class="btn w-full rounded-sm bg-rr-red px-6 py-3 label-font text-sm text-white glow-red hover:bg-rr-red-lt disabled:opacity-50 disabled:cursor-not-allowed"
    >
      {#if submitting}
        Saving...
      {:else}
        Continue to payment
      {/if}
    </button>
  </form>
</div>
