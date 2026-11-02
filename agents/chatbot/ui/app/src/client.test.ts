import { createKitClient } from "@declarative-agents/ui-kit";
import { afterEach, describe, expect, it } from "vitest";
import { sendAudit, sendChat } from "./api";
import { appClient, setAppClient } from "./client";
import { fetchSourceTypes } from "./documentsApi";

// Every call the SPA makes goes through the kit client (srd004 R6.1, GH-782),
// whose base URL is injectable. These prove it by pointing the app at a client
// with a recording fetch and a non-empty base URL: a call that still used a raw
// fetch would reach the bare path and record nothing here.

interface RecordedCall {
  url: string;
  method: string;
  headers: Headers;
  body?: string;
}

function recordingClient(baseUrl: string, respond: (url: string) => Response) {
  const calls: RecordedCall[] = [];
  const fetch: typeof globalThis.fetch = async (input, init) => {
    calls.push({
      url: String(input),
      method: init?.method ?? "GET",
      headers: new Headers(init?.headers),
      body: typeof init?.body === "string" ? init.body : undefined,
    });
    return respond(String(input));
  };
  return { client: createKitClient({ baseUrl, fetch }), calls };
}

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "content-type": "application/json" },
  });
}

const original = appClient();

afterEach(() => setAppClient(original));

describe("the app's calls run through the kit client", () => {
  it("sends a chat turn to the client's base URL with its clearance header", async () => {
    const { client, calls } = recordingClient("https://mesh.example", () =>
      jsonResponse({ answer: "grounded", sources: [] }),
    );
    setAppClient(client);

    await sendChat({ message: "what changed?", history: [] }, undefined, "secret");

    expect(calls).toHaveLength(1);
    expect(calls[0].url).toBe("https://mesh.example/api/v1/chat");
    expect(calls[0].method).toBe("POST");
    expect(calls[0].headers.get("x-analyst-clearance")).toBe("secret");
    expect(JSON.parse(calls[0].body ?? "{}")).toMatchObject({ message: "what changed?" });
  });

  it("sends an audit through the same client", async () => {
    const { client, calls } = recordingClient("https://mesh.example", () =>
      jsonResponse({ verdict: "supported" }),
    );
    setAppClient(client);

    const audit = await sendAudit("summary", "document");

    expect(audit.verdict).toBe("supported");
    expect(calls[0].url).toBe("https://mesh.example/api/v1/audit");
  });

  it("reads documents through the same client", async () => {
    const { client, calls } = recordingClient("https://mesh.example", () =>
      jsonResponse({ documents: [] }),
    );
    setAppClient(client);

    await fetchSourceTypes("unclassified");

    expect(calls).toHaveLength(1);
    expect(calls[0].url.startsWith("https://mesh.example/")).toBe(true);
    expect(calls[0].headers.get("x-analyst-clearance")).toBe("unclassified");
  });
});
