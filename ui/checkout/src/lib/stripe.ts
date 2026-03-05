import { loadStripe, type Stripe, type StripeElements } from '@stripe/stripe-js';

let stripePromise: Promise<Stripe | null> | null = null;

export function getStripe(publishableKey: string): Promise<Stripe | null> {
  if (!stripePromise) {
    stripePromise = loadStripe(publishableKey);
  }
  return stripePromise;
}

export function createElements(stripe: Stripe, clientSecret: string): StripeElements {
  return stripe.elements({
    clientSecret,
    appearance: {
      theme: 'stripe',
      variables: {
        colorPrimary: '#2D7A7A',
        borderRadius: '6px',
      },
    },
  });
}
