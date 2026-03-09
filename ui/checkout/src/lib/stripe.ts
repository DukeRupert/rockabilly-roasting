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
      theme: 'night',
      variables: {
        colorPrimary: '#B82018',
        colorBackground: '#161A1E',
        colorText: '#D8E4E8',
        colorTextSecondary: '#8A9EA8',
        colorDanger: '#D42820',
        fontFamily: 'Barlow, sans-serif',
        borderRadius: '2px',
        colorInputBackground: '#161A1E',
        colorInputText: '#D8E4E8',
      },
    },
  });
}
