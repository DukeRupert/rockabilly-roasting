// One line of the box being signed up for. Mirrors internal/web/subscribe.go's
// subscribeLineRequest — a variant, the plan it renews on, and how many.
export interface SubscribeLineRequest {
  plan_id: string;
  variant_id: string;
  quantity: number;
}

export interface SubscribePaymentIntentRequest {
  lines: SubscribeLineRequest[];
  email: string;
  first_name: string;
  last_name: string;
  line1: string;
  line2?: string;
  city: string;
  state: string;
  postal_code: string;
  country: string;
  /**
   * The PI the client is abandoning (address edited after the payment
   * element mounted, or a line added/removed). The server cancels it so its
   * pre-created order is cleaned up via the payment_intent.canceled webhook.
   */
  previous_payment_intent_id?: string;
}

// SubscribeLinePriced is what one line of the box actually costs, read back
// from the server rather than computed here: nothing is priced in the
// browser. Matched to a box line by plan_id and variant_id together: one
// variant on two plans is two lines at two prices, and a match on the variant
// alone puts both prices on one of them.
export interface SubscribeLinePriced {
  plan_id: string;
  variant_id: string;
  unit_price: number;
  quantity: number;
  subtotal: number;
}

export interface SubscribePaymentIntentResponse {
  client_secret: string;
  /** Total being charged: item subtotal + shipping + tax. */
  amount: number;
  currency: string;
  subtotal: number;
  shipping_total: number;
  shipping_label?: string;
  tax_total: number;
  tax_label?: string;
  lines: SubscribeLinePriced[];
}

export interface SubscribeConfirmRequest {
  payment_intent_id: string;
}

export interface SubscribeConfirmResponse {
  /** Every subscription the signup started. */
  subscription_ids?: string[];
  /** Set only when there is exactly one. */
  subscription_id?: string;
  order_id: string;
  /**
   * "active" when the subscription exists; "processing" when payment is
   * settling asynchronously and the webhook will activate it server-side.
   */
  status: 'active' | 'processing';
}

async function request<T>(url: string, options?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    headers: { 'Content-Type': 'application/json' },
    ...options,
  });
  const data = await res.json();
  if (!res.ok) {
    throw new Error(data.error || `Request failed: ${res.status}`);
  }
  return data as T;
}

export interface SubscribePrefill {
  email: string;
  phone?: string;
  first_name?: string;
  last_name?: string;
  line1?: string;
  line2?: string;
  city?: string;
  state?: string;
  postal_code?: string;
  country?: string;
}

export interface SubscribeContextResponse {
  prefill: SubscribePrefill | null;
}

// getSubscribeContext returns the signed-in customer's contact + default
// address to prefill the form. Resolves to null prefill for guests; callers
// treat any failure as "no prefill" and never block the form on it.
export function getSubscribeContext(): Promise<SubscribeContextResponse> {
  return request<SubscribeContextResponse>('/api/subscribe/context');
}

export function createSubscribePaymentIntent(
  req: SubscribePaymentIntentRequest,
): Promise<SubscribePaymentIntentResponse> {
  return request<SubscribePaymentIntentResponse>('/api/subscribe/payment-intent', {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

export function confirmSubscription(
  req: SubscribeConfirmRequest,
): Promise<SubscribeConfirmResponse> {
  return request<SubscribeConfirmResponse>('/api/subscribe/confirm', {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

// One item the "Add another item" picker can offer, and the plans it can put
// that item on. Fed by GET /api/subscribe/catalog: the retail catalog's
// subscribable, priced variants.
export interface SubscribeCatalogVariant {
  variant_id: string;
  product_title: string;
  variant_label: string;
  base_price: number;
}

export interface SubscribeCatalogPlan {
  plan_id: string;
  name: string;
  discount_pct: number;
}

export interface SubscribeCatalogResponse {
  variants: SubscribeCatalogVariant[];
  plans: SubscribeCatalogPlan[];
}

export function getSubscribeCatalog(): Promise<SubscribeCatalogResponse> {
  return request<SubscribeCatalogResponse>('/api/subscribe/catalog');
}
