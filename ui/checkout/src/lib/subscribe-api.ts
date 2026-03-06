export interface SubscribePaymentIntentRequest {
  plan_id: string;
  variant_id: string;
  email: string;
  first_name: string;
  last_name: string;
  line1: string;
  line2?: string;
  city: string;
  state: string;
  postal_code: string;
  country: string;
}

export interface SubscribePaymentIntentResponse {
  client_secret: string;
  amount: number;
  currency: string;
}

export interface SubscribeConfirmRequest {
  plan_id: string;
  variant_id: string;
  email: string;
  first_name: string;
  last_name: string;
  line1: string;
  line2?: string;
  city: string;
  state: string;
  postal_code: string;
  country: string;
  payment_intent_id: string;
}

export interface SubscribeConfirmResponse {
  subscription_id: string;
  order_id: string;
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
