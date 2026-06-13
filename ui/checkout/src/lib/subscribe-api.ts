export interface SubscribePaymentIntentRequest {
  plan_id: string;
  variant_id: string;
  quantity: number;
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
   * element mounted). The server cancels it so its pre-created order is
   * cleaned up via the payment_intent.canceled webhook.
   */
  previous_payment_intent_id?: string;
}

export interface SubscribePaymentIntentResponse {
  client_secret: string;
  amount: number;
  currency: string;
}

export interface SubscribeConfirmRequest {
  payment_intent_id: string;
}

export interface SubscribeConfirmResponse {
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
