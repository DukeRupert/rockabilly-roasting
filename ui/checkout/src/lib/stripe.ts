import { loadStripe, type Stripe, type StripeElements } from '@stripe/stripe-js';

let stripePromise: Promise<Stripe | null> | null = null;

export function getStripe(publishableKey: string): Promise<Stripe | null> {
  if (!stripePromise) {
    stripePromise = loadStripe(publishableKey);
  }
  return stripePromise;
}

// Paper-and-ink appearance for Stripe Elements:
// - Flat theme (no ambient shadows or glows; we draw borders ourselves)
// - Oswald-ish default font, ink text on cream-hi surfaces
// - Rust primary, rust danger (amber is reserved for highlights, not UI state)
export function createElements(stripe: Stripe, clientSecret: string): StripeElements {
  return stripe.elements({
    clientSecret,
    appearance: {
      theme: 'flat',
      variables: {
        colorPrimary: '#B4351D',
        colorBackground: '#FFFBF1',
        colorText: '#0E0D0C',
        colorTextSecondary: '#8E887D',
        colorDanger: '#B4351D',
        fontFamily: 'Oswald, Arial Narrow, sans-serif',
        fontSizeBase: '14px',
        borderRadius: '0px',
        spacingUnit: '4px',
        colorInputBackground: '#FFFBF1',
        colorInputText: '#0E0D0C',
      },
      rules: {
        '.Input': {
          border: '2px solid #0E0D0C',
          boxShadow: 'none',
          padding: '10px 14px',
        },
        '.Input:focus': {
          border: '2px solid #B4351D',
          boxShadow: '0 0 0 2px rgba(180, 53, 29, 0.2)',
          outline: 'none',
        },
        '.Label': {
          fontWeight: '700',
          letterSpacing: '0.2em',
          textTransform: 'uppercase',
          fontSize: '11px',
          color: '#0E0D0C',
        },
        '.Tab': {
          border: '2px solid #0E0D0C',
          boxShadow: 'none',
          padding: '12px 16px',
        },
        '.Tab--selected': {
          border: '2px solid #0E0D0C',
          backgroundColor: '#ECE0C6',
          boxShadow: '2px 2px 0 0 #0E0D0C',
        },
        '.Error': {
          fontWeight: '700',
          color: '#B4351D',
        },
      },
    },
  });
}
