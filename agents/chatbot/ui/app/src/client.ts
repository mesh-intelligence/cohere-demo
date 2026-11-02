import { createKitClient, type KitClient } from "@declarative-agents/ui-kit";

// The one client every call in this SPA goes through (srd004 R6.1, GH-782).
// Its base URL is injectable, so the same panels run same-origin in the pod and
// against another origin under a shell later; nothing else in the app performs
// I/O, which is what keeps that true.
let client: KitClient = createKitClient();

export function appClient(): KitClient {
  return client;
}

// setAppClient points the app at another client. Tests use it to record what a
// domain call sent without reaching the network.
export function setAppClient(next: KitClient): void {
  client = next;
}
