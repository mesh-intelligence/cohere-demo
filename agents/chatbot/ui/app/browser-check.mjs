// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0
//
// The browser gate for the chatbot SPA (rel02.0-uc001 S1). Everything else that
// covers this UI reads it without rendering it: vitest exercises pure functions
// and the scenario critic rig drives the agents' HTTP surfaces. Two defects
// reached deployment through that gap -- a permanent error panel for a RAG unit
// the release does not deploy (GH-233), and, after that fix, a 404 reissued
// every three seconds behind a panel that looked correct (GH-243).
//
// The second one is why this file measures request rates rather than only
// reading the DOM. A page can render exactly right while hammering an endpoint,
// and no assertion about the screen will ever notice.
//
// Driven by `mage integration:ui`, which resolves Chrome and the base URL and
// skips when either is missing. Run directly with:
//   CHATBOT_UI_BASE=http://cohere-demo.localhost \
//   CHROME_PATH=/path/to/chrome node browser-check.mjs [screenshot.png]

import puppeteer from "puppeteer-core";

const BASE = process.env.CHATBOT_UI_BASE || "http://cohere-demo.localhost";
const OBSERVER_BASE = process.env.OBSERVER_UI_BASE || "http://observer.cohere-demo.localhost";
const CHROME = process.env.CHROME_PATH;
const SCREENSHOT = process.argv[2];

// The corpus manifest's own primary retrieval condition (corpus/corpus.yaml
// retrieval_conditions), so a turn that retrieves nothing retrieved nothing
// rather than something else. The previous default named the retired
// synthetic corpus's entities and had been asserting grounding against
// material the tasking never mentioned.
const TASKING =
  process.env.CHATBOT_UI_TASKING ||
  "Prepare a command-staff brief on Russian strikes on Ukrainian infrastructure in December 2022 and the air-defence response.";

// The agent the shipped list carries but a single-unit deployment does not run.
// Its probe is the GH-243 measurement, so the gate needs to know its name.
const ABSENT_AGENT = process.env.CHATBOT_UI_ABSENT_AGENT || "rag1";

// The top of the declared ordered clearance set. The gate runs the same tasking
// at the default lowest level and at this one, so the filter's effect is visible
// as a difference between two turns rather than asserted about one.
const HIGHEST_CLEARANCE = process.env.CHATBOT_UI_HIGHEST_CLEARANCE || "secret";

// A turn is the longest inference chain in the mesh -- embed, select sources,
// fan out, rerank, compose -- against live providers. Minutes, not seconds.
const TURN_TIMEOUT_MS = Number(process.env.CHATBOT_UI_TURN_TIMEOUT_MS || 300000);

// Long enough to span several live poll intervals, so a panel that re-probes on
// the live cadence is caught rather than sampled between requests.
const DWELL_MS = Number(process.env.CHATBOT_UI_DWELL_MS || 15000);

if (!CHROME) {
  console.error("CHROME_PATH is not set; mage integration:ui resolves it and skips when absent");
  process.exit(2);
}

const results = [];
// everySectionIsNotApplicable reads a brief that kept the template's headings
// and wrote N/A under each: the conformant way to say nothing was found.
function everySectionIsNotApplicable(answer) {
  const bodies = answer.split(/^##[^\n]*$/m).slice(1).map((body) => body.trim());
  return bodies.length > 0 && bodies.every((body) => /^N\/A\.?$/i.test(body));
}

function check(name, pass, detail) {
  results.push({ name, pass, detail });
  console.log(`${pass ? "PASS" : "FAIL"}  ${name}${detail ? " — " + detail : ""}`);
}

const absentProbePattern = new RegExp(`monitor-proxy/${ABSENT_AGENT}/`);

const browser = await puppeteer.launch({
  executablePath: CHROME,
  headless: "new",
  args: ["--no-sandbox", "--disable-dev-shm-usage"],
});

try {
  const page = await browser.newPage();
  await page.setViewport({ width: 1440, height: 900 });

  // Every request the page makes, kept for the store-isolation check: the
  // browser must reach the mesh's boundaries and never the vector store
  // itself (srd016 R5.2). The store's API path shape is what identifies it.
  const requestedURLs = [];
  page.on("request", (r) => requestedURLs.push(r.url()));

  // Collected across the whole run: a page that renders correctly while
  // throwing, or while retrying an endpoint that will never answer, is broken
  // in a way no snapshot of the DOM reports.
  const consoleErrors = [];
  const failedRequests = [];
  page.on("console", (m) => {
    if (m.type() === "error") consoleErrors.push(m.text());
  });
  page.on("response", (r) => {
    if (r.status() >= 400) failedRequests.push(`${r.status()} ${r.url()}`);
  });

  // ---- Observability panels (rel02.0-uc001 S1) ----
  await page.goto(`${BASE}/ui/observability`, { waitUntil: "networkidle2", timeout: 60000 });
  // The panel decides absence on its first 404, so read after at least one poll.
  await new Promise((r) => setTimeout(r, 8000));

  const panels = await page.$$eval(".agent-panel .agent-name", (els) =>
    els.map((e) => e.textContent.trim()),
  );
  check("a panel renders for each deployed agent", panels.length > 0, `panels: ${panels.join(", ")}`);

  // GH-233. The shipped agent list is a superset of any one deployment, and an
  // agent the release does not deploy must render nothing rather than a
  // permanent error panel.
  check(
    "no panel for an agent the release does not deploy",
    !panels.some((p) => new RegExp(ABSENT_AGENT, "i").test(p.replace(/\s+/g, ""))) &&
      !panels.some((p) => /RAG server 1/i.test(p)),
    `panels: ${panels.join(", ")}`,
  );
  const bodyText = await page.$eval("body", (b) => b.innerText);
  check("no HTTP error text is rendered on the page", !/HTTP \d{3}/.test(bodyText), "");
  const errorDots = await page.$$eval(".dot-err", (els) => els.length);
  check("no agent panel is in the error state", errorDots === 0, `dot-err count: ${errorDots}`);

  // GH-243. The panel looked correct while re-probing the absent agent every
  // three seconds. Dwelling and counting is the only assertion that sees it.
  const probesBefore = failedRequests.filter((r) => absentProbePattern.test(r)).length;
  await new Promise((r) => setTimeout(r, DWELL_MS));
  const probesDuringDwell =
    failedRequests.filter((r) => absentProbePattern.test(r)).length - probesBefore;
  check(
    `an absent agent is re-probed at most once in ${Math.round(DWELL_MS / 1000)}s`,
    probesDuringDwell <= 1,
    `${probesDuringDwell} probe(s) during the dwell`,
  );

  // ---- The /search command (GH-449) ----
  // Retrieval alone through the search machine: type the command, get a
  // result list with provenance, no brief. Runs before the grounded turn so
  // the window is empty and the palette is the first thing exercised.
  await page.goto(`${BASE}/ui/chat`, { waitUntil: "networkidle2", timeout: 60000 });
  await page.waitForSelector(".chat-input textarea", { timeout: 30000 });
  const paletteChips = await page.$$eval(".command-chip", (els) => els.map((e) => e.textContent));
  check(
    "the command palette names the workflow vocabulary",
    paletteChips.some((c) => c.includes("/search")) && paletteChips.some((c) => c.includes("/make-brief")),
    `chips: ${paletteChips.join(" ")}`,
  );
  await page.type(".chat-input textarea", `/search ${TASKING}`);
  await page.click(".chat-input-box button");
  await page.waitForSelector(".search-results, .search-results-empty, .bubble-error", {
    timeout: TURN_TIMEOUT_MS,
  });
  const searchRows = await page.$$eval(".search-result", (els) => els.length);
  const searchErrors = await page.$$eval(".bubble-error", (els) => els.length);
  check("/search returned retrieved chunks with provenance", searchRows > 0 && searchErrors === 0,
    `${searchRows} row(s), ${searchErrors} error bubble(s)`);

  // ---- The /help command (GH-497) ----
  // The workflow figure returns on demand, mid-conversation, with no server call.
  await page.type(".chat-input textarea", "/help");
  await page.click(".chat-input-box button");
  await page.waitForSelector(".bubble-assistant .turn-figure", { timeout: 10000 });
  check(
    "/help repeats the workflow figure in the conversation",
    (await page.$(".bubble-assistant .turn-figure svg")) !== null,
    "",
  );

  // ---- Chat history survives a reload (GH-509) ----
  // The /help turn above is in the client-side history; a fresh load lists it
  // in the sidebar and clicking it restores the conversation.
  await page.goto(`${BASE}/ui/chat`, { waitUntil: "networkidle2", timeout: 60000 });
  await page.waitForSelector(".chat-history-item", { timeout: 10000 });
  await page.click(".chat-history-item");
  await page.waitForSelector(".bubble-assistant .turn-figure", { timeout: 10000 });
  check(
    "a reloaded page lists previous chats and restores one",
    (await page.$$eval(".bubble", (els) => els.length)) >= 2,
    `${await page.$$eval(".chat-history-item", (els) => els.length)} chat(s) listed`,
  );

  // ---- Chat history delete and the demo reset (GH-588) ----
  // A seeded second chat proves the × removes exactly its own entry, and the
  // Ctrl-Shift-R chord clears the whole store (the browser's hard reload then
  // lands on an empty app; headless asserts the cleared store directly).
  await page.evaluate(() => {
    const key = "chatbot-chat-history-v1";
    const chats = JSON.parse(localStorage.getItem(key) ?? "[]");
    chats.push({
      id: "chat-gate-delete-me",
      title: "gate: delete me",
      startedAt: Date.now() - 86400000,
      messages: [],
    });
    localStorage.setItem(key, JSON.stringify(chats));
  });
  await page.goto(`${BASE}/ui/chat`, { waitUntil: "networkidle2", timeout: 60000 });
  await page.waitForSelector(".chat-history-delete", { timeout: 10000 });
  const rowsBeforeDelete = await page.$$eval(".chat-history-row", (els) => els.length);
  await page.evaluate(() => {
    const row = [...document.querySelectorAll(".chat-history-row")].find((r) =>
      r.textContent.includes("gate: delete me"),
    );
    row.querySelector(".chat-history-delete").click();
  });
  await page.waitForFunction(
    (before) => document.querySelectorAll(".chat-history-row").length === before - 1,
    { timeout: 5000 },
    rowsBeforeDelete,
  );
  const deleted = await page.evaluate(() => {
    const chats = JSON.parse(localStorage.getItem("chatbot-chat-history-v1") ?? "[]");
    return {
      seededGone: !chats.some((chat) => chat.id === "chat-gate-delete-me"),
      remaining: chats.length,
    };
  });
  check(
    "the sidebar × deletes exactly its own stored chat",
    deleted.seededGone && deleted.remaining > 0,
    `seeded entry gone: ${deleted.seededGone}, ${deleted.remaining} chat(s) remain`,
  );
  await page.keyboard.down("Control");
  await page.keyboard.down("Shift");
  await page.keyboard.press("KeyR");
  await page.keyboard.up("Shift");
  await page.keyboard.up("Control");
  // The chord may also fire the browser's own hard reload; either way the
  // store was cleared first, so settle and assert the surviving state.
  await new Promise((resolve) => setTimeout(resolve, 700));
  const resetState = await page.evaluate(() => ({
    stored: localStorage.getItem("chatbot-chat-history-v1"),
    rows: document.querySelectorAll(".chat-history-row").length,
  }));
  check(
    "Ctrl-Shift-R clears the stored chat history (demo reset)",
    resetState.stored === null && resetState.rows === 0,
    `store: ${resetState.stored === null ? "cleared" : "still present"}, ${resetState.rows} row(s) listed`,
  );

  // ---- The /eval command (GH-475, srd014 R8) ----
  // One tasking through the shipped turn; the bubble reports recall with the
  // corpus identity and the synthetic-corpus label in the message itself.
  const bubblesBeforeEval = await page.$$eval(".bubble", (els) => els.length);
  await page.type(".chat-input textarea", "/eval 1");
  await page.click(".chat-input-box button");
  await page.waitForFunction(
    (before) =>
      document.querySelectorAll(".bubble").length >= before + 2 &&
      !document.querySelector(".bubble-pending"),
    { timeout: TURN_TIMEOUT_MS },
    bubblesBeforeEval,
  );
  const evalReport = await page.evaluate(() => {
    const bubbles = document.querySelectorAll(".bubble-assistant:not(.bubble-pending)");
    const last = bubbles[bubbles.length - 1];
    return {
      text: last ? last.querySelector(".bubble-text").textContent.trim() : "",
      error: last ? last.classList.contains("bubble-error") : true,
    };
  });
  check(
    "/eval reports a labeled recall figure from the generated corpus",
    !evalReport.error &&
      /Retrieval recall \d\.\d{3}/.test(evalReport.text) &&
      evalReport.text.includes("evaluation-generated-") &&
      evalReport.text.includes("synthetic demonstration corpus"),
    `${evalReport.text.slice(0, 140)}${evalReport.error ? " (error bubble)" : ""}`,
  );

  // ---- The /harvest command (GH-459) ----
  // Search, embed, and ingest into the scratch corpus: the receipt bubble
  // names what landed, and the next demo seed drops it.
  const bubblesBeforeHarvest = await page.$$eval(".bubble", (els) => els.length);
  await page.type(".chat-input textarea", "/harvest december strike wave reporting");
  await page.click(".chat-input-box button");
  await page.waitForFunction(
    (before) =>
      document.querySelectorAll(".bubble").length >= before + 2 &&
      !document.querySelector(".bubble-pending"),
    { timeout: TURN_TIMEOUT_MS },
    bubblesBeforeHarvest,
  );
  const harvest = await page.evaluate(() => {
    const bubbles = document.querySelectorAll(".bubble-assistant:not(.bubble-pending)");
    const last = bubbles[bubbles.length - 1];
    return {
      text: last ? last.querySelector(".bubble-text").textContent.trim() : "",
      error: last ? last.classList.contains("bubble-error") : true,
    };
  });
  check(
    "/harvest ingested web results and reported the tool-called queries",
    !harvest.error &&
      /Harvested \d+ web result/.test(harvest.text) &&
      harvest.text.includes("harvest-") &&
      /proposed \d+ (query|queries) by tool call/.test(harvest.text),
    `${harvest.text.slice(0, 120)}${harvest.error ? " (error bubble)" : ""}`,
  );

  // The harvest receipt carries its own trace button (GH-615), and the
  // overlay's walk shows the tool-call derivation and the search fan.
  const harvestWalk = await (async () => {
    for (let attempt = 0; attempt < 5; attempt++) {
      const buttons = await page.$$(".bubble-trace");
      if (buttons.length === 0) return "";
      await buttons[buttons.length - 1].click();
      // The agent grouping lives on the Agents tab (GH-644, GH-652).
      await page.waitForSelector(".trace-tabs", { timeout: 15000 }).catch(() => {});
      await page.evaluate(() => {
        [...document.querySelectorAll(".trace-tab")].find((t) => t.textContent.trim() === "Agents")?.click();
      });
      // The collector spools spans after the turn answers; a group that has
      // not arrived yet is a retry, never a failure (the GH-513 pattern).
      const arrived = await page
        .waitForSelector(".trace-overlay .agent-group", { timeout: 15000 })
        .then(() => true)
        .catch(() => false);
      if (arrived) {
        await page.evaluate(() => {
          for (const head of document.querySelectorAll(".agent-group-head")) head.click();
        });
        await new Promise((r) => setTimeout(r, 200));
      }
      const text = arrived ? await page.$eval(".trace-overlay", (el) => el.textContent) : "";
      await page.click(".trace-overlay-close").catch(() => {});
      if (text.includes("derive_search_terms_via_tool")) return text;
      await new Promise((r) => setTimeout(r, 5000));
    }
    return "";
  })();
  check(
    "the harvest trace walk shows the tool call and the search fan",
    harvestWalk.includes("derive_search_terms_via_tool") && harvestWalk.includes("osint_search"),
    harvestWalk ? "tool-call and search words present in the walk" : "walk never showed the tool call",
  );

  // ---- Model-proposed source tiers (GH-694) ----
  // A tasking naming a collection discipline, with no facet checkbox set,
  // narrows retrieval to that tier through the selection's proposal; a
  // checked box always wins (the faceted checks above are that case).
  let proposed = { corpus: [], hr: 0 };
  for (let attempt = 0; attempt < 3; attempt++) {
    proposed = await page.evaluate(async () => {
      const res = await fetch("/api/v1/chat", {
        method: "POST",
        headers: { "content-type": "application/json", "X-Analyst-Clearance": "secret" },
        body: JSON.stringify({
          message: "Prepare a brief from HUMINT liaison field reporting on the Patriot air-defence decision",
          history: [],
        }),
      });
      const data = await res.json();
      const docs = data.metadata?.composition?.documents ?? [];
      const corpus = docs.filter((d) => !d.startsWith("osint-") && !d.startsWith("harvest-"));
      return { corpus, hr: corpus.filter((d) => d.startsWith("HR-")).length };
    });
    if (proposed.corpus.length > 0 && proposed.hr === proposed.corpus.length) break;
    await new Promise((r) => setTimeout(r, 3000));
  }
  check(
    "a discipline-naming tasking narrows retrieval by the selection's proposed tiers",
    proposed.corpus.length > 0 && proposed.hr === proposed.corpus.length,
    `corpus docs: ${proposed.corpus.slice(0, 6).join(", ")} (${proposed.hr}/${proposed.corpus.length} HR)`,
  );

  // ---- Model-proposed date windows (GH-730) ----
  // A tasking naming a period bounds retrieval by the chunks' date_key; every
  // composed ISW document's id carries its issued date, so the window shows
  // in the ids themselves.
  let windowed = { docs: [], inWindow: 0 };
  for (let attempt = 0; attempt < 3; attempt++) {
    windowed = await page.evaluate(async () => {
      const res = await fetch("/api/v1/chat", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          message:
            "Prepare a brief on Russian strikes on Ukrainian infrastructure between 16 and 18 December 2022",
          history: [],
        }),
      });
      const data = await res.json();
      const docs = (data.metadata?.composition?.documents ?? []).filter((d) => d.startsWith("isw-"));
      const inWindow = docs.filter((d) => /^isw-2022-12-1[678]/.test(d)).length;
      return { docs, inWindow };
    });
    if (windowed.docs.length > 0 && windowed.inWindow === windowed.docs.length) break;
    await new Promise((r) => setTimeout(r, 3000));
  }
  check(
    "a period-naming tasking bounds retrieval to the proposed date window",
    windowed.docs.length > 0 && windowed.inWindow === windowed.docs.length,
    `composed: ${windowed.docs.slice(0, 6).join(", ")} (${windowed.inWindow}/${windowed.docs.length} in window)`,
  );
  // An out-of-range period retrieves nothing and the brief says so instead
  // of composing a confident answer from another year's reporting.
  const outOfRange = await page.evaluate(async () => {
    const res = await fetch("/api/v1/chat", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        message:
          "Prepare a command-staff brief on strikes on infrastructure and the air defence response in 1968",
        history: [],
      }),
    });
    const data = await res.json();
    const composed = (data.metadata?.sources?.composed ?? []).reduce(
      (sum, entry) => sum + (entry.ids ?? []).reduce((n, list) => n + list.length, 0),
      0,
    );
    return {
      composed,
      citations: (data.metadata?.citations ?? []).length,
      answer: (data.answer ?? "").slice(0, 400),
    };
  });
  check(
    "an out-of-range period composes nothing and the brief admits it",
    outOfRange.composed === 0 &&
      outOfRange.citations === 0 &&
      // The refusal wording varies run to run; any of the honest forms passes,
      // including the template-conformant brief whose every section reads N/A.
      (/unable|cannot|not available|no (information|reporting|data)/i.test(outOfRange.answer) ||
        everySectionIsNotApplicable(outOfRange.answer)),
    `${outOfRange.composed} composed row(s), ${outOfRange.citations} citation(s); answer: ${outOfRange.answer.slice(0, 100).replace(/\n/g, " ")}`,
  );

  // ---- The reset purge (GH-648) ----
  // The Ctrl-Shift-R beacon posts to this endpoint; the gate calls it
  // directly and proves the harvested rows leave the corpus while the seeded
  // rows stay retrievable.
  const purgeResponse = await page.evaluate(async () => {
    const res = await fetch("/api/v1/harvest/purge", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: '{"reason": "gate"}',
    });
    return { status: res.status, body: await res.text() };
  });
  let purgedSearch = { total: 0, harvested: -1 };
  for (let attempt = 0; attempt < 5; attempt++) {
    purgedSearch = await page.evaluate(async () => {
      const res = await fetch("/api/v1/search", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ message: "strikes on energy infrastructure" }),
      });
      const data = await res.json();
      const ids = (data.results ?? []).map((row) => row.id ?? row.record_id ?? "");
      return { total: ids.length, harvested: ids.filter((id) => id.startsWith("harvest-")).length };
    });
    if (purgedSearch.total > 0 && purgedSearch.harvested === 0) break;
    await new Promise((r) => setTimeout(r, 2000));
  }
  check(
    "the reset purge drops harvested rows and keeps the seeded corpus",
    purgeResponse.status === 200 && purgedSearch.total > 0 && purgedSearch.harvested === 0,
    `purge: ${purgeResponse.status} ${purgeResponse.body.slice(0, 60)}; search: ${purgedSearch.total} row(s), ${purgedSearch.harvested} harvested`,
  );

  // ---- A grounded turn ----
  await page.goto(`${BASE}/ui/chat`, { waitUntil: "networkidle2", timeout: 60000 });
  await page.waitForSelector(".chat-input textarea", { timeout: 30000 });
  await page.type(".chat-input textarea", TASKING);
  await page.click(".chat-input-box button");
  check("the turn was accepted and is pending", (await page.$(".bubble-pending")) !== null, "");
  await page.waitForSelector(".bubble-assistant:not(.bubble-pending)", { timeout: TURN_TIMEOUT_MS });

  const turn = await page.evaluate(() => {
    const answer = document.querySelector(".bubble-assistant:not(.bubble-pending) .bubble-text");
    return {
      answerLength: answer ? answer.textContent.trim().length : 0,
      chips: [...document.querySelectorAll(".source-chip")].map((c) => c.textContent.trim()),
      // The record ids the brief cited. The source chip names the unit and its
      // document count, which is the same at every level; only the records
      // differ, so the records are what the level comparison has to read.
      cited: [
        ...new Set(
          [...document.querySelectorAll(".citation-chip:not(.citation-chip-dropped)")].map((c) =>
            c.textContent.trim(),
          ),
        ),
      ],
      degraded: document.querySelectorAll(".degraded").length,
      errors: document.querySelectorAll(".bubble-error").length,
    };
  });
  check("the turn answered with prose", turn.answerLength > 200, `${turn.answerLength} chars`);
  check("the answer is grounded in a source", turn.chips.length > 0, `chips: ${turn.chips.join(" | ")}`);
  // The chip reads "rag0 · 5 documents": it names the unit and how much it
  // retrieved, so a source that participated and contributed nothing is visible
  // as a source that contributed nothing.
  check(
    "the source chip reports retrieved documents",
    turn.chips.some((c) => /\d+\s+documents?/.test(c)),
    `chips: ${turn.chips.join(" | ")}`,
  );
  check("the turn is not marked degraded", turn.degraded === 0, `degraded nodes: ${turn.degraded}`);
  check("no error bubble", turn.errors === 0, `error bubbles: ${turn.errors}`);

  // Cited spans in the INTSUM carry the source's Admiralty grade inline
  // (GH-535): captured now, while the grounded turn's form is on screen.
  const inlineGrades = await page.$$eval(".citation-grade", (els) =>
    els.map((e) => e.textContent.trim()),
  );
  check(
    "cited spans carry the source's Admiralty grade inline",
    inlineGrades.length > 0 && inlineGrades.every((g) => /^\([A-F][1-6](·[A-F][1-6])*\)$/.test(g)),
    `${inlineGrades.length} grade mark(s): ${[...new Set(inlineGrades)].slice(0, 5).join(" ")}`,
  );

  // Clicking a cited-span mark opens the provenance walk for its record
  // (GH-598) — the underline is a promise, and the walk is what it means.
  const walkableMarks = await page.$$eval(".intsum-cited-walkable", (els) => els.length);
  await page.click(".intsum-cited-walkable");
  await page.waitForSelector(".provenance", { timeout: 30000 });
  check(
    "clicking a cited span opens the provenance walk",
    walkableMarks > 0 && (await page.$(".provenance")) !== null,
    `${walkableMarks} walkable mark(s); provenance panel open`,
  );
  // Back to the form so the checks that follow see the state they expect.
  await page.click(".provenance-back");
  await page.waitForSelector(".intsum-form", { timeout: 15000 });

  // ---- The /audit command (GH-543, GH-650) ----
  // The grounded turn's brief is on screen; each sentence judged whole
  // against its cited chunks, reported as a table weakest first.
  // The aside's Audit tab (GH-660): empty before any audit, offering the
  // run button beside the Report tab.
  const emptyAudit = await page.evaluate(async () => {
    const tabs = [...document.querySelectorAll(".aside-tab")];
    tabs.find((t) => t.textContent.trim() === "Audit")?.click();
    await new Promise((r) => setTimeout(r, 200));
    const out = {
      tabs: tabs.map((t) => t.textContent.trim()),
      emptyNote: Boolean(document.querySelector(".audit-empty")),
      runButton: Boolean(document.querySelector(".audit-run:not([disabled])")),
      formHidden: !document.querySelector(".intsum-form"),
    };
    tabs.find((t) => t.textContent.trim() === "Report")?.click();
    return out;
  });
  check(
    "the Audit tab sits beside Report, empty with a run button before any audit",
    emptyAudit.tabs.join(",") === "Report,Audit" &&
      emptyAudit.emptyNote &&
      emptyAudit.runButton &&
      emptyAudit.formHidden,
    `tabs: ${emptyAudit.tabs.join(", ")}; empty: ${emptyAudit.emptyNote}; button: ${emptyAudit.runButton}`,
  );
  const bubblesBeforeAudit = await page.$$eval(".bubble", (els) => els.length);
  await page.type(".chat-input textarea", "/audit 6");
  await page.click(".chat-input-box button");
  await page.waitForFunction(
    (before) =>
      document.querySelectorAll(".bubble").length >= before + 2 &&
      !document.querySelector(".bubble-pending"),
    { timeout: TURN_TIMEOUT_MS },
    bubblesBeforeAudit,
  );
  const auditReport = await page.evaluate(() => {
    const bubbles = document.querySelectorAll(".bubble-assistant:not(.bubble-pending)");
    const last = bubbles[bubbles.length - 1];
    return {
      text: last ? last.querySelector(".bubble-text").textContent.trim() : "",
      error: last ? last.classList.contains("bubble-error") : true,
      trace: last ? Boolean(last.querySelector(".bubble-trace")) : false,
    };
  });
  check(
    "/audit scores each sentence against its cited chunks",
    !auditReport.error && /Citation audit: \d+ of \d+ sentences scored/.test(auditReport.text),
    `${auditReport.text.slice(0, 140)}${auditReport.error ? " (error bubble)" : ""}`,
  );
  // The summary bubble carries the first judge run's trace, so the audit
  // turn lands in the Traces overlay's chip list (GH-720).
  check("the /audit summary bubble carries a trace button", auditReport.trace, "");
  // The report table (GH-650): rows weakest first, sources as provenance
  // links so a low grade's reason sits beside what the chunk says.
  const auditTable = await page.evaluate(() => {
    const rows = [...document.querySelectorAll(".audit-table tbody tr")];
    const scores = rows
      .map((row) => Number(row.querySelector(".audit-score")?.textContent))
      .filter((score) => Number.isFinite(score));
    return {
      rows: rows.length,
      links: document.querySelectorAll(".audit-table .provenance-link").length,
      reasons: rows.filter((row) => (row.querySelector(".audit-reason")?.textContent.trim() ?? "") !== "").length,
      sortedWeakestFirst: scores.every((score, index) => index === 0 || score >= scores[index - 1]),
    };
  });
  check(
    "/audit lands the aside on the Audit tab",
    await page.$eval(".aside-tab-active", (el) => el.textContent.trim()) === "Audit",
    "",
  );
  // A scored row's trace opens the judge run itself (GH-662): its steps
  // carry the audit machine's own commands.
  const auditTrace = await page.evaluate(async () => {
    // The collector spools spans after the run answers, and the overlay's
    // fetch settles once — so reopen the overlay per attempt to refetch
    // (the GH-513 pattern, one level up).
    let judge = false;
    let opened = false;
    const btn = document.querySelector(".audit-trace");
    for (let attempt = 0; attempt < 6 && !judge; attempt++) {
      btn?.click();
      await new Promise((r) => setTimeout(r, 4000));
      opened = opened || Boolean(document.querySelector('.trace-overlay[aria-label="Trace story window"]'));
      judge = [...document.querySelectorAll(".step-group-command")].some((e) =>
        e.textContent.trim().startsWith("judge_with_cohere"),
      );
      document.querySelector(".trace-overlay-close")?.click();
      if (!judge) await new Promise((r) => setTimeout(r, 3000));
    }
    return { hasButton: Boolean(btn), opened, judge };
  });
  check(
    "an audit row's trace opens the judge run with its machine steps",
    auditTrace.hasButton && auditTrace.opened && auditTrace.judge,
    `button: ${auditTrace.hasButton}; opened: ${auditTrace.opened}; judge step: ${auditTrace.judge}`,
  );
  check(
    "the audit table lists sentences weakest first with chunk links and reasons",
    auditTable.rows > 0 &&
      auditTable.links > 0 &&
      auditTable.reasons === auditTable.rows &&
      auditTable.sortedWeakestFirst,
    `${auditTable.rows} row(s), ${auditTable.links} link(s), ${auditTable.reasons} reason(s), sorted: ${auditTable.sortedWeakestFirst}`,
  );

  // ---- The /refine command (GH-621) ----
  // The audit findings steer an automatic revision, or the command reports
  // the brief stands; both are correct terminals and the gate accepts either.
  const bubblesBeforeRefine = await page.$$eval(".bubble", (els) => els.length);
  await page.type(".chat-input textarea", "/refine");
  await page.click(".chat-input-box button");
  await page.waitForFunction(
    (before) => {
      const bubbles = [...document.querySelectorAll(".bubble-assistant:not(.bubble-pending)")];
      const last = bubbles[bubbles.length - 1];
      if (!last || document.querySelector(".bubble-pending")) return false;
      const text = last.textContent;
      // Standing verdict ends the turn; a revision ends with a new brief.
      return /the brief stands/.test(text) || document.querySelectorAll(".bubble").length >= before + 3;
    },
    { timeout: TURN_TIMEOUT_MS },
    bubblesBeforeRefine,
  );
  const refined = await page.evaluate(() => {
    const bubbles = [...document.querySelectorAll(".bubble-assistant:not(.bubble-pending)")];
    const texts = bubbles.map((b) => b.textContent);
    return {
      reported: texts.some((t) => /Refine pass \d+\/\d+: audited \d+ of \d+ sentences/.test(t)),
      stood: texts.some((t) => /the brief stands/.test(t)),
      revised: texts.some((t) => /Revising against \d+ weak sentence/.test(t)),
      statusBadged: [...document.querySelectorAll(".bubble-assistant")].some(
        (b) => /Refine pass/.test(b.textContent) && b.querySelector(".unattributed"),
      ),
      errors: document.querySelectorAll(".bubble-error").length,
    };
  });
  check(
    "/refine audits the brief and either revises against the findings or lets it stand",
    refined.reported && (refined.stood || refined.revised) && refined.errors === 0 && !refined.statusBadged,
    `stood: ${refined.stood}, revised: ${refined.revised}, status badge leak: ${refined.statusBadged}`,
  );

  // ---- /summarize, /revise, and /export over the brief on screen (GH-451) ----
  // The grounded turn left a brief showing, which is exactly the state the
  // three remaining commands need: /summarize reads it, plain text defaults
  // to /revise against it, and /export renders it.
  const bubblesBeforeSummary = await page.$$eval(".bubble", (els) => els.length);
  await page.type(".chat-input textarea", "/summarize");
  await page.click(".chat-input-box button");
  await page.waitForFunction(
    (before) =>
      document.querySelectorAll(".bubble").length >= before + 2 &&
      !document.querySelector(".bubble-pending"),
    { timeout: TURN_TIMEOUT_MS },
    bubblesBeforeSummary,
  );
  const summary = await page.evaluate(() => {
    const bubbles = document.querySelectorAll(".bubble-assistant:not(.bubble-pending)");
    const last = bubbles[bubbles.length - 1];
    return {
      text: last ? last.querySelector(".bubble-text").textContent.trim() : "",
      error: last ? last.classList.contains("bubble-error") : true,
    };
  });
  check(
    "/summarize answered with prose from the Command model",
    !summary.error && summary.text.length > 80,
    `${summary.text.length} chars${summary.error ? ", error bubble" : ""}`,
  );

  // Plain text with a brief on screen is the /revise default: the parser
  // routes it to the revision turn, which regenerates a conformant brief.
  // The note is the deck's analyst-story prompt: the first brief covers the
  // strikes and the air defence response but not the reported Russian losses,
  // so the revision must pull the sigint_series losses tier into the form.
  const bubblesBeforeRevise = await page.$$eval(".bubble", (els) => els.length);
  await page.type(
    ".chat-input textarea",
    "The brief is missing the reported Russian personnel and equipment losses over the same period. Add a section on them.",
  );
  await page.click(".chat-input-box button");
  await page.waitForFunction(
    (before) =>
      document.querySelectorAll(".bubble").length >= before + 2 &&
      !document.querySelector(".bubble-pending"),
    { timeout: TURN_TIMEOUT_MS },
    bubblesBeforeRevise,
  );
  const revised = await page.evaluate(() => {
    const bubbles = document.querySelectorAll(".bubble-assistant:not(.bubble-pending)");
    const last = bubbles[bubbles.length - 1];
    const form = document.querySelector(".intsum-form");
    return {
      length: last ? last.querySelector(".bubble-text").textContent.trim().length : 0,
      error: last ? last.classList.contains("bubble-error") : true,
      chips: last ? last.querySelectorAll(".source-chip").length : 0,
      mentionsLosses: form ? /loss/i.test(form.textContent) : false,
      lossCitations: last
        ? [...last.querySelectorAll(".citation-chip")].filter((c) =>
            /^losses-/i.test(c.textContent.trim()),
          ).length
        : 0,
      // Marks on the revised form itself: GH-577's defect was the added
      // section landing under an undeclared heading and being dropped from
      // the form, which left zero marks on every revised brief.
      lossMarks: [...document.querySelectorAll(".intsum-cited")].filter((m) =>
        /losses-/i.test(m.getAttribute("title") ?? ""),
      ).length,
    };
  });
  check(
    "plain text with a brief showing runs the /revise default and re-grounds",
    !revised.error && revised.length > 200 && revised.chips > 0,
    `${revised.length} chars, ${revised.chips} source chip(s)`,
  );
  check(
    "the analyst-story revision adds the missing losses material, cited to the losses tier",
    revised.mentionsLosses && revised.lossCitations > 0,
    `losses in form: ${revised.mentionsLosses}, losses- citation chip(s): ${revised.lossCitations}`,
  );
  check(
    "the revised form marks the losses spans with clickable citations (GH-577)",
    revised.lossMarks > 0,
    `${revised.lossMarks} intsum-cited mark(s) citing losses- records`,
  );

  // The changed-text tint (GH-605): a revision tints what the note changed,
  // and the toggle hides the tint without touching the text.
  const tinted = await page.$$eval(".intsum-revision-new", (els) => els.length);
  await page.click(".intsum-changes-toggle");
  const afterToggle = await page.$$eval(".intsum-revision-new", (els) => els.length);
  await page.click(".intsum-changes-toggle");
  check(
    "the revision tints its changed text, and the toggle hides it",
    tinted > 0 && afterToggle === 0,
    `${tinted} tinted segment(s), ${afterToggle} after toggling off`,
  );

  // ---- The report language selector (GH-670) ----
  // A rendition of the approved text: the French view keeps the citation
  // links and the Source Evaluation rows, and names the English record as
  // authoritative.
  const englishSample = await page.evaluate(() => ({
    body: document.querySelector(".intsum-form")?.textContent.slice(0, 4000) ?? "",
    evalRows: document.querySelectorAll(".intsum-table tbody tr").length,
    links: document.querySelectorAll(".intsum-form .provenance-link").length,
  }));
  await page.evaluate(() => {
    const select = document.querySelector(".report-language select");
    const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, "value").set;
    setter.call(select, "fr");
    select.dispatchEvent(new Event("change", { bubbles: true }));
  });
  await page.waitForFunction(
    () => {
      const note = document.querySelector(".translate-note");
      return note && /authoritative/.test(note.textContent);
    },
    { timeout: 120000 },
  );
  const french = await page.evaluate(() => ({
    body: document.querySelector(".intsum-form")?.textContent.slice(0, 4000) ?? "",
    evalRows: document.querySelectorAll(".intsum-table tbody tr").length,
    links: document.querySelectorAll(".intsum-form .provenance-link").length,
  }));
  check(
    "the French rendition keeps citations and gradings while the text changes",
    french.body !== englishSample.body &&
      french.evalRows === englishSample.evalRows &&
      french.links > 0 &&
      (await page.$(".translate-note .bubble-trace")) !== null,
    `rows ${englishSample.evalRows} -> ${french.evalRows}; links ${englishSample.links} -> ${french.links}; changed: ${french.body !== englishSample.body}`,
  );
  // Back to the authoritative record for the checks below.
  await page.evaluate(() => {
    const select = document.querySelector(".report-language select");
    const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, "value").set;
    setter.call(select, "en");
    select.dispatchEvent(new Event("change", { bubbles: true }));
  });
  await new Promise((r) => setTimeout(r, 500));

  // The /clearance command sets the analyst level from the chat line (GH-678).
  const bubblesBeforeClearance = await page.$$eval(".bubble", (els) => els.length);
  await page.type(".chat-input textarea", "/clearance confidential");
  await page.click(".chat-input-box button");
  await page.waitForFunction(
    (before) => document.querySelectorAll(".bubble").length >= before + 2,
    { timeout: 15000 },
    bubblesBeforeClearance,
  );
  const clearanceCmd = await page.evaluate(() => ({
    text: ([...document.querySelectorAll(".bubble-assistant")].pop()?.textContent ?? "").slice(0, 80),
    value: [...document.querySelectorAll("select")].find((sel) =>
      [...sel.options].some((o) => o.value === "confidential"),
    )?.value,
  }));
  check(
    "/clearance sets the analyst level from the chat line",
    /Analyst clearance set to confidential/.test(clearanceCmd.text) && clearanceCmd.value === "confidential",
    `${clearanceCmd.text}; selector: ${clearanceCmd.value}`,
  );
  await page.evaluate(() => {
    const sel = [...document.querySelectorAll("select")].find((s2) => s2.value === "confidential");
    const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, "value").set;
    setter.call(sel, "unclassified");
    sel.dispatchEvent(new Event("change", { bubbles: true }));
  });

  // The /translate command drives the same rendition path from the chat line
  // (GH-676).
  const bubblesBeforeTranslate = await page.$$eval(".bubble", (els) => els.length);
  await page.type(".chat-input textarea", "/translate german");
  await page.click(".chat-input-box button");
  await page.waitForFunction(
    (before) => document.querySelectorAll(".bubble").length >= before + 2 && !document.querySelector(".bubble-pending"),
    { timeout: 180000 },
    bubblesBeforeTranslate,
  );
  // The confirmation's trace button lands with the run's trace id, a moment
  // after its text, so the sample waits for it rather than racing it.
  await page
    .waitForFunction(
      () =>
        [...document.querySelectorAll(".bubble-assistant")].some(
          (b) => /Rendered the brief in/.test(b.textContent ?? "") && b.querySelector(".bubble-trace"),
        ),
      { timeout: 30000 },
    )
    .catch(() => {});
  const translateCmd = await page.evaluate(() => {
    // The confirmation is found by what it says rather than by being last: a
    // brief re-rendered after it would take the last slot and fail a check that
    // popped, which is the race this assertion used to lose.
    const bubbles = [...document.querySelectorAll(".bubble-assistant")];
    const confirmation = bubbles.reverse().find((b) => /Rendered the brief in/.test(b.textContent ?? ""));
    return {
      text: (confirmation ?? bubbles[0])?.textContent?.slice(0, 120) ?? "",
      error: confirmation?.classList.contains("bubble-error") ?? true,
      note: Boolean(document.querySelector(".translate-note .bubble-trace")),
      trace: Boolean(confirmation?.querySelector(".bubble-trace")),
    };
  });
  check(
    "/translate renders the rendition from the chat line",
    !translateCmd.error && /Rendered the brief in German/.test(translateCmd.text) && translateCmd.note,
    `${translateCmd.text}; note+trace: ${translateCmd.note}`,
  );
  // The confirmation bubble carries the translate run's trace, so the turn
  // lands in the Traces overlay's chip list (GH-720).
  check("the /translate bubble carries a trace button", translateCmd.trace, "");
  await page.evaluate(() => {
    const select = document.querySelector(".report-language select");
    const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, "value").set;
    setter.call(select, "en");
    select.dispatchEvent(new Event("change", { bubbles: true }));
  });
  await new Promise((r) => setTimeout(r, 500));

  // The panel's own export button renders the same full document (GH-631).
  await page.evaluate(() => { window.open = () => null; });
  await page.click(".export-submit");
  await page.waitForSelector(".export-result", { timeout: 60000 });
  const buttonRender = await page.$eval(".export-result", (el) => el.textContent);
  check(
    "the export button renders the full form to a PDF",
    /rendered \d+ KB/.test(buttonRender),
    buttonRender,
  );

  // /export fires the export relay and confirms in the window; the download
  // itself is the relay's PDF, asserted by the confirmation rather than the
  // browser's save pipeline.
  const bubblesBeforeExport = await page.$$eval(".bubble", (els) => els.length);
  // The viewer tab is browser chrome headless cannot meaningfully show, and a
  // headless popup can stall the opener's CDP session; the check asserts the
  // confirmation, so the open is stubbed (GH-461).
  await page.evaluate(() => {
    window.open = () => null;
  });
  await page.type(".chat-input textarea", "/export");
  await page.click(".chat-input-box button");
  await page.waitForFunction(
    (before) => document.querySelectorAll(".bubble").length >= before + 2,
    { timeout: TURN_TIMEOUT_MS },
    bubblesBeforeExport,
  );
  const exported = await page.evaluate(() => {
    const bubbles = document.querySelectorAll(".bubble-assistant:not(.bubble-pending)");
    const last = bubbles[bubbles.length - 1];
    return {
      text: last ? last.querySelector(".bubble-text").textContent.trim() : "",
      error: last ? last.classList.contains("bubble-error") : true,
    };
  });
  check(
    "/export rendered the brief as a PDF and confirmed",
    !exported.error && /intsum\.pdf/.test(exported.text),
    exported.text.slice(0, 80),
  );

  // ---- The sidebar Traces entry and the turn's own trace button ----
  //
  // Traces open two ways (GH-513, GH-640): the sidebar entry opens the story
  // window over the active panel, defaulting to the newest chat's last turn,
  // and each answer bubble opens it focused on that turn.
  check(
    "the sidebar carries a Traces entry",
    (await page.$$eval(".nav-item", (els) => els.map((e) => e.textContent.trim()))).includes("Traces"),
    "",
  );
  await page.evaluate(() => {
    [...document.querySelectorAll(".nav-item")].find((e) => e.textContent.trim() === "Traces")?.click();
  });
  await page.waitForSelector('.trace-overlay[aria-label="Trace story window"]', { timeout: 15000 });
  const sidebarStory = await page.evaluate(() => ({
    chips: document.querySelectorAll(".trace-turn-chips .turn-chip").length,
    focused: document.querySelectorAll(".turn-chip-active").length,
  }));
  check(
    "the sidebar entry opens the story window on the newest chat's turns",
    sidebarStory.chips >= 1 && sidebarStory.focused === 1,
    `${sidebarStory.chips} chip(s), ${sidebarStory.focused} focused`,
  );
  await page.click(".trace-overlay-close");
  await page.waitForSelector(".bubble-trace", { timeout: 10000 });
  // The collector spools spans after the turn answers; reopen the overlay
  // until the story's terminal rows arrived (the same poll demo:verifyTrace
  // does server-side).
  for (let attempt = 0; attempt < 4; attempt++) {
    // The newest bubble-trace can be a /translate or /audit run (GH-720) —
    // small single-agent traces. The deep-dive below asserts the full mesh
    // story, so click the newest brief turn's button instead.
    await page.evaluate(() => {
      const briefs = [...document.querySelectorAll(".bubble-assistant")].filter(
        (bubble) =>
          bubble.querySelector(".bubble-trace") &&
          !/Rendered the brief|Citation audit/.test(bubble.textContent),
      );
      briefs[briefs.length - 1].querySelector(".bubble-trace").click();
    });
    await page.waitForSelector(".trace-overlay .timeline-seq-row", { timeout: 60000 });
    const done = await page.evaluate(() => {
      const cats = new Set([...document.querySelectorAll(".seq-category")].map((e) => e.textContent.trim()));
      return cats.has("response") && cats.has("handoff");
    });
    if (done || attempt === 3) break;
    await page.click(".trace-overlay-close");
    await new Promise((r) => setTimeout(r, 4000));
  }

  // The story window (GH-517): the sequence rows carry category badges so the
  // flow reads input -> calls -> handoff -> response, and the chat picker
  // scopes the window to the launching chat by default.
  const story = await page.evaluate(() => ({
    categories: [...new Set([...document.querySelectorAll(".seq-category")].map((e) => e.textContent.trim()))],
    picker: document.querySelector(".trace-chat-picker .detail-toggle")?.textContent.trim() ?? "",
    turnChips: document.querySelectorAll(".trace-turn-chips .turn-chip").length,
  }));
  check(
    "the sequence rows wear story categories — input, calls, handoff, response",
    // The kit's vocabulary (GH-737): it splits what this app called a tool run
    // into agent call, store call, and external call, so the flow-defining
    // categories are asserted rather than the whole set.
    story.categories.includes("input") &&
      story.categories.includes("model call") &&
      story.categories.includes("tool call") &&
      story.categories.includes("handoff") &&
      story.categories.includes("response"),
    `categories: ${story.categories.join(", ")}`,
  );
  check(
    "the story window scopes to the launching chat with a picker for more",
    story.picker.startsWith("chats shown: 1") && story.turnChips >= 1,
    `picker: ${story.picker}; ${story.turnChips} turn chip(s)`,
  );
  // ---- The agent timeline (GH-511) ----
  // Rows per active message generator (external peers included), a brush that
  // zooms a window and drops inactive lanes, and the window as a top-to-bottom
  // call sequence.
  await page.waitForSelector(".timeline-lane", { timeout: 15000 });
  const timeline = await page.evaluate(() => ({
    lanes: [...document.querySelectorAll(".timeline-lane-name")].map((e) => e.textContent.trim()),
    external: document.querySelectorAll(".timeline-lane-external").length,
    seqRows: document.querySelectorAll(".timeline-seq-row").length,
    targets: [...document.querySelectorAll(".timeline-seq-target")].map((e) => e.textContent).slice(0, 4),
  }));
  check(
    "the timeline rows are the agents, external peers included",
    timeline.lanes.some((l) => l.startsWith("chatbot")) &&
      timeline.lanes.some((l) => l.startsWith("rag0")) &&
      timeline.external >= 1 &&
      timeline.lanes.some((l) => l.includes("api.cohere.com")),
    `lanes: ${timeline.lanes.join(" | ")}`,
  );
  check(
    "the window reads top to bottom as the sequence of calls with their targets",
    timeline.seqRows > 30 && timeline.targets.some((t) => t.includes("api.cohere.com")),
    `${timeline.seqRows} row(s); targets: ${timeline.targets.join(" ")}`,
  );
  const lanesBeforeZoom = timeline.lanes.length;
  await page.evaluate(() => {
    const handle = document.querySelector(".timeline-handle-end");
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value").set;
    setter.call(handle, "40");
    handle.dispatchEvent(new Event("input", { bubbles: true }));
    handle.dispatchEvent(new Event("change", { bubbles: true }));
  });
  const zoomed = await page.evaluate(() => document.querySelectorAll(".timeline-lane").length);
  await page.click(".timeline-reset");
  check(
    "zooming the brush filters to the agents active in the window",
    zoomed <= lanesBeforeZoom,
    `${lanesBeforeZoom} lane(s) -> ${zoomed} in the first 4% of the trace`,
  );

  // ---- Steps and Agents tabs (GH-644, GH-652) ----
  const tabsDefault = await page.evaluate(() => ({
    labels: [...document.querySelectorAll(".trace-tab")].map((e) => e.textContent.trim()),
    active: document.querySelector(".trace-tab-active")?.textContent.trim() ?? "",
    agentGroups: document.querySelectorAll(".agent-group").length,
    stepGroups: document.querySelectorAll(".step-group").length,
  }));
  check(
    "the overlay opens on the Steps tab, grouped by workflow step",
    tabsDefault.labels.join(",") === "Steps,Agents" &&
      tabsDefault.active === "Steps" &&
      tabsDefault.stepGroups > 1 &&
      tabsDefault.agentGroups === 0,
    `tabs: ${tabsDefault.labels.join(", ")}; active: ${tabsDefault.active}; ${tabsDefault.stepGroups} step group(s)`,
  );
  // A row click opens the span's content — for the composition span, the
  // captured prompt itself (GH-658): a user message carrying the documents.
  const detailCheck = await page.evaluate(async () => {
    const row = [...document.querySelectorAll(".step-group .span-row")].find((r) =>
      r.textContent.includes("invoke_cohere_command"),
    ) ?? document.querySelector(".step-group .span-row");
    row?.click();
    await new Promise((r) => setTimeout(r, 200));
    const detail = document.querySelector(".span-detail");
    const userMessage = [...(detail?.querySelectorAll(".span-message") ?? [])].find(
      (m) => m.querySelector("summary")?.textContent.trim() === "user",
    );
    const assistantMessage = [...(detail?.querySelectorAll(".span-message") ?? [])].find(
      (m) => m.querySelector("summary")?.textContent.trim() === "assistant",
    );
    const out = {
      open: Boolean(detail),
      title: detail?.querySelector(".span-detail-title")?.textContent ?? "",
      attrs:
        (detail?.querySelectorAll(".span-attr").length ?? 0) +
        (detail?.querySelectorAll(".span-attr-long").length ?? 0) +
        (detail?.querySelectorAll(".span-messages").length ?? 0),
      promptChars: userMessage?.querySelector("pre")?.textContent.length ?? 0,
      // The captured response (upstream GH-95): the composition span's
      // http.response.body renders as an open assistant block carrying the
      // brief's text (GH-728).
      answerOpen: assistantMessage?.hasAttribute("open") ?? false,
      answerChars: assistantMessage?.querySelector("pre")?.textContent.length ?? 0,
      answerRows: document.querySelectorAll(".span-row-answer").length,
    };
    detail?.querySelector(".trace-overlay-close")?.click();
    return out;
  });
  check(
    "clicking the composition step opens the span with its captured prompt",
    detailCheck.open && detailCheck.title !== "" && detailCheck.attrs > 0 && detailCheck.promptChars > 100,
    `open: ${detailCheck.open}; ${detailCheck.attrs} attribute(s), prompt ${detailCheck.promptChars} chars on ${detailCheck.title.slice(0, 40)}`,
  );
  check(
    "the composition span's drawer shows the brief's text as an open assistant block",
    detailCheck.answerOpen && detailCheck.answerChars > 100,
    `assistant open: ${detailCheck.answerOpen}, ${detailCheck.answerChars} chars`,
  );
  check(
    "model calls carry inline answer rows in the step view",
    detailCheck.answerRows > 0,
    `${detailCheck.answerRows} answer row(s)`,
  );
  // The model's answer arrives unfolded (GH-726) — also on an invoke_llm
  // span, which carries it in gen_ai.output.messages rather than the REST
  // capture the composition span uses.
  const answerCheck = await page.evaluate(async () => {
    const row = [...document.querySelectorAll(".step-group .span-row")].find((r) =>
      r.textContent.includes("derive_search_terms"),
    );
    row?.click();
    await new Promise((r) => setTimeout(r, 200));
    const detail = document.querySelector(".span-detail");
    const assistant = [...(detail?.querySelectorAll(".span-message") ?? [])].find(
      (m) => m.querySelector("summary")?.textContent.trim() === "assistant",
    );
    const out = {
      row: Boolean(row),
      answerOpen: assistant?.hasAttribute("open") ?? false,
      answerChars: assistant?.querySelector("pre")?.textContent.length ?? 0,
    };
    detail?.querySelector(".trace-overlay-close")?.click();
    return out;
  });
  check(
    "the model's answer renders unfolded in an invoke_llm span's drawer",
    answerCheck.row && answerCheck.answerOpen && answerCheck.answerChars > 0,
    `row: ${answerCheck.row}; assistant open: ${answerCheck.answerOpen}, ${answerCheck.answerChars} chars`,
  );
  await page.evaluate(() => {
    [...document.querySelectorAll(".trace-tab")].find((t) => t.textContent.trim() === "Agents")?.click();
  });
  await page.waitForSelector(".agent-group", { timeout: 15000 });
  const agentsTab = await page.evaluate(() => ({
    stepGroups: document.querySelectorAll(".step-group").length,
    agentGroups: document.querySelectorAll(".agent-group").length,
    names: [...document.querySelectorAll(".agent-group-name")].map((e) => e.textContent.trim()),
  }));
  check(
    "the Agents tab groups the same window by agent and hides the step view",
    agentsTab.agentGroups > 1 && agentsTab.stepGroups === 0 && agentsTab.names.some((n) => n.startsWith("rag0")),
    `${agentsTab.agentGroups} agent group(s): ${agentsTab.names.join(", ")}`,
  );
  // Unfold the chatbot's group and follow a handoff to its continuation.
  const followCheck = await page.evaluate(async () => {
    const heads = [...document.querySelectorAll(".agent-group-head")];
    heads.find((h) => h.textContent.includes("chatbot"))?.click();
    await new Promise((r) => setTimeout(r, 200));
    const rows = document.querySelectorAll(".agent-group .span-row").length;
    const follow = [...document.querySelectorAll(".span-follow")].find((f) =>
      f.textContent.includes("continues at"),
    );
    const label = follow?.textContent ?? "";
    follow?.click();
    await new Promise((r) => setTimeout(r, 300));
    const selected = document.querySelector(".timeline-seq-selected");
    return {
      rows,
      label,
      landedService: selected?.querySelector(".trace-svc")?.textContent.trim() ?? "",
    };
  });
  check(
    "a handoff link lands on the continuation span in the other agent's group",
    followCheck.rows > 20 &&
      /continues at/.test(followCheck.label) &&
      followCheck.landedService !== "" &&
      followCheck.label.includes(followCheck.landedService),
    `${followCheck.rows} chatbot row(s); ${followCheck.label.slice(0, 50)} -> landed on ${followCheck.landedService}`,
  );
  // Close the sticky content drawer so the span-tree clicks below land on
  // their rows rather than on the drawer covering them.
  await page.evaluate(() => {
    document.querySelector(".span-detail .trace-overlay-close")?.click();
  });

  // The span tree stays available behind its toggle (GH-423 via GH-511).
  for (const button of await page.$$(".detail-toggle")) {
    if ((await button.evaluate((e) => e.textContent.trim())) === "show the span tree") {
      await button.click();
      break;
    }
  }
  await page.waitForSelector(".trace-row", { timeout: 10000 });
  const treeBefore = await page.evaluate(() => ({
    rows: document.querySelectorAll(".trace-row").length,
    toggles: document.querySelectorAll(".trace-toggle[aria-expanded]").length,
    services: new Set(
      [...document.querySelectorAll(".trace-row .trace-svc")].map((e) => e.textContent.trim()),
    ).size,
  }));
  check(
    "the trace renders as a span tree across the mesh's agents",
    treeBefore.rows > 20 && treeBefore.toggles > 0 && treeBefore.services >= 2,
    `${treeBefore.rows} rows, ${treeBefore.toggles} toggle(s), ${treeBefore.services} service(s)`,
  );
  // Each call carries its declared one-line description, and clicking a
  // command chip filters to every call that does the same thing (GH-426).
  const described = await page.evaluate(() => ({
    withDescription: document.querySelectorAll(".trace-row .trace-desc").length,
    firstCommand:
      document.querySelector(".trace-row .trace-command")?.textContent.trim() ?? "",
  }));
  check(
    "trace rows carry the declared description of the call",
    described.withDescription > 10,
    `${described.withDescription} row(s) described`,
  );
  if (described.firstCommand) {
    const countRows = (command) =>
      page.evaluate((c) => ({
        rows: document.querySelectorAll(".trace-row").length,
        matching: [...document.querySelectorAll(".trace-row .trace-command")].filter(
          (chip) => chip.textContent.trim() === c,
        ).length,
      }), command);
    const unfiltered = await countRows(described.firstCommand);
    await page.click(".trace-row .trace-command");
    const filtered = await countRows(described.firstCommand);
    check(
      // The filter keeps every call of the clicked command and drops the rest,
      // except the ancestors that keep the tree rooted -- so the property is
      // that no matching call is lost and the tree shrinks, not that every
      // remaining row matches.
      "clicking a command filters to every call doing the same thing",
      filtered.matching === unfiltered.matching &&
        filtered.matching > 0 &&
        filtered.rows < unfiltered.rows,
      `command ${described.firstCommand}: ${filtered.matching}/${unfiltered.matching} matching kept,` +
        ` ${filtered.rows} of ${unfiltered.rows} row(s) shown`,
    );
    // Clear the filter so the fold assertions below see the whole tree.
    await page.click(".trace-row .trace-command");
  }

  for (const button of await page.$$(".trace-fold button")) {
    if ((await button.evaluate((e) => e.textContent.trim())) === "collapse to roots") {
      await button.click();
      break;
    }
  }
  const treeAfter = await page.evaluate(() => document.querySelectorAll(".trace-row").length);
  check(
    "collapsing to roots folds the subtrees away",
    treeAfter < treeBefore.rows,
    `${treeBefore.rows} rows -> ${treeAfter}`,
  );

  const groupCommands = await page.evaluate(() => {
    const chatbot = [...document.querySelectorAll(".agent-group")].find((g) =>
      g.querySelector(".agent-group-name")?.textContent.startsWith("chatbot"),
    );
    return [...(chatbot?.querySelectorAll(".timeline-seq-call") ?? [])].map((e) => e.textContent.trim());
  });
  check(
    "the chatbot group carries the machine's own commands",
    groupCommands.some((c) => c.startsWith("embed_query")) && groupCommands.some((c) => c.startsWith("compose_")),
    `${groupCommands.length} command row(s)`,
  );

  await page.click(".trace-overlay-close");

  // ---- A faceted turn at the lowest level (srd016 R4; rel11.0-uc001 S1) ----
  //
  // The clearance comparison runs under the humint source-type facet at both
  // levels rather than unfaceted: the corpus's relevant set spans levels
  // within that tier (HR-22-019 and HR-22-014 are secret), while the
  // unfaceted top-N for the primary tasking can land entirely on unclassified
  // material and show no difference. Narrowing the pool is what makes the
  // window's effect visible in the five records a turn composes.
  // A live provider composes every turn but does not cite every turn -- a
  // rate-limited chat leg degrades to the local fallback, which never runs
  // the citation chain. Two consequences for the harness: turns are paced and
  // a turn that cited nothing is retried after a pause, and every assertion
  // that can rest on retrieval reads the source-evaluation table instead of
  // the citations, because retrieval is the mesh's own work while a citation
  // is the provider's mood.
  const PACE_MS = Number(process.env.CHATBOT_UI_PACE_MS || 45000);
  const pace = () => new Promise((r) => setTimeout(r, PACE_MS));
  const runFacetedTurn = async (clearance, { facet = true, requireLinks = false } = {}) => {
    let turnResult;
    for (let attempt = 1; attempt <= 3; attempt++) {
      await page.goto(`${BASE}/ui/chat`, { waitUntil: "networkidle2", timeout: 60000 });
      await page.waitForSelector(".clearance-select select", { timeout: 30000 });
      await page.select(".clearance-select select", clearance);
      // The source-type vocabulary arrives from the corpus through the document
      // boundary (srd016 R4.5); its chips render only after that read answers.
      // The source-type facet renders as a checkbox per type (GH-379); it was a
      // chip until then, and the selector outliving the markup cost this gate
      // everything after it (GH-400).
      await page.waitForFunction(
        () => [...document.querySelectorAll(".facet-check")].some((c) => c.textContent.trim() === "humint"),
        { timeout: 30000 },
      );
      if (facet) {
        for (const chip of await page.$$(".facet-check")) {
          if ((await chip.evaluate((e) => e.textContent.trim())) === "humint") {
            await chip.click();
            break;
          }
        }
      }
      await page.type(".chat-input textarea", TASKING);
      await page.click(".chat-input-box button");
      await page.waitForSelector(".bubble-assistant:not(.bubble-pending)", { timeout: TURN_TIMEOUT_MS });
      turnResult = await page.evaluate(() => {
        const evalSection = [...document.querySelectorAll(".intsum-section")].find(
          (section) => section.querySelector("h3")?.textContent.trim() === "Source Evaluation",
        );
        return {
          cited: [
            ...new Set(
              [...document.querySelectorAll(".citation-chip:not(.citation-chip-dropped)")].map((c) =>
                c.textContent.trim(),
              ),
            ),
          ],
          // Corpus citations alone: the external chips are the search branch's,
          // which the facets deliberately do not reach (srd016 R4.2; srd011).
          citedCorpus: [
            ...new Set(
              [
                ...document.querySelectorAll(
                  ".citation-chip:not(.citation-chip-dropped):not(.citation-chip-external)",
                ),
              ].map((c) => c.textContent.trim()),
            ),
          ],
          citationLinks: document.querySelectorAll(".intsum-form .provenance-link").length,
          evalSources: [...(evalSection?.querySelectorAll("tbody td:first-child") ?? [])].map((e) =>
            e.textContent.trim(),
          ),
          evalHeaders: [...(evalSection?.querySelectorAll("thead th") ?? [])].map((e) => e.textContent.trim()),
          evalMarkings: [...(evalSection?.querySelectorAll("tbody td:nth-child(2)") ?? [])].map((e) =>
            e.textContent.trim(),
          ),
          enforced: [...document.querySelectorAll(".clearance-enforced")].map((e) => e.textContent.trim()),
          legendChips: [...document.querySelectorAll(".intsum-marking-chip")].map((e) => e.textContent.trim()),
          tintedMarks: document.querySelectorAll(
            ".intsum-cited-restricted, .intsum-cited-confidential, .intsum-cited-secret",
          ).length,
          body: document.body.innerText,
        };
      });
      if (requireLinks ? turnResult.citationLinks > 0 : turnResult.cited.length > 0) break;
      console.log(`      (turn at ${clearance} ${requireLinks ? "offered no corpus citation link" : "cited nothing"} on attempt ${attempt}; pausing and retrying)`);
      await pace();
    }
    return turnResult;
  };

  const facetedLow = await runFacetedTurn("unclassified");
  check(
    "the faceted turn at the lowest level composes only the faceted tier",
    facetedLow.evalSources.length > 0 && facetedLow.evalSources.every((id) => id.startsWith("HR-")),
    `sources: ${facetedLow.evalSources.join(", ") || "none"}`,
  );
  // Every evaluated source wears its classification marking (GH-646): the
  // column sits beside the source id and no cell is left blank.
  check(
    "the source evaluation table marks each source's classification",
    facetedLow.evalHeaders.includes("Classification") &&
      facetedLow.evalMarkings.length === facetedLow.evalSources.length &&
      facetedLow.evalMarkings.every((m) => m.length > 0),
    `headers: ${facetedLow.evalHeaders.join(", ")}; markings: ${facetedLow.evalMarkings.join(", ") || "none"}`,
  );
  check(
    "the faceted turn at the lowest level reports its enforcement",
    facetedLow.enforced.some((e) => e.includes("unclassified")),
    `enforced: ${facetedLow.enforced.join(" | ")}`,
  );
  await pace();

  // ---- The two-panel INTSUM surface (rel11.5-uc001 S1, srd016 R1) ----
  //
  // The route the turn above ran on: two columns, no fourth route, and a
  // narrow viewport collapsing to one column with the form reachable.
  const layout = await page.evaluate(() => {
    const columns = document.querySelector(".chat-columns");
    return {
      tracks: columns ? getComputedStyle(columns).gridTemplateColumns.split(" ").length : 0,
      hasForm: document.querySelector(".intsum-column") !== null,
      navItems: [...document.querySelectorAll(".nav-item")].map((e) => e.textContent.trim()),
      observerLink:
        document.querySelector(".nav-item-external[href*='observer.cohere-demo.localhost']")?.getAttribute("target") ?? "",
    };
  });
  check("the chat route renders two columns", layout.tracks === 2, `tracks: ${layout.tracks}`);
  check("the form column sits beside the tasking", layout.hasForm, "");
  // The claim is that the form lives beside the tasking rather than on a
  // route of its own (srd016 R1) -- not that the shell never gains another
  // panel. A count here broke when the provisioning panel shipped as a
  // declared route (GH-402).
  check(
    "the nav links the fleet observer at the host the chart pins",
    layout.observerLink === "_blank",
    layout.observerLink === "" ? "no external observer link in the nav" : `target: ${layout.observerLink}`,
  );
  check(
    "no route was added for the form",
    layout.navItems.includes("Chat") &&
      !layout.navItems.some((label) => /intsum|form|brief/i.test(label)),
    `nav: ${layout.navItems.join(", ")}`,
  );
  await page.setViewport({ width: 700, height: 900 });
  const narrow = await page.evaluate(() => {
    const columns = document.querySelector(".chat-columns");
    const form = document.querySelector(".intsum-column");
    return {
      tracks: columns ? getComputedStyle(columns).gridTemplateColumns.split(" ").length : 0,
      formVisible: form !== null && form.getBoundingClientRect().width > 0,
    };
  });
  check(
    "a narrow viewport collapses to one column with the form reachable",
    narrow.tracks === 1 && narrow.formVisible,
    `tracks: ${narrow.tracks}, form visible: ${narrow.formVisible}`,
  );
  await page.setViewport({ width: 1440, height: 900 });

  // ---- A faceted turn at the highest level fills the form (srd016 R3, R4) ----
  //
  // The same faceted tasking at the highest declared level. The corpus keys
  // every humint record id with the HR- source prefix, so the facet's
  // pre-search effect is visible in the interface as every citation resolving
  // to that tier -- an assertion about what was admitted, not about what a
  // request returned (R8.3).
  const facetedHigh = await runFacetedTurn(HIGHEST_CLEARANCE);
  const faceted = {
    ...facetedHigh,
    ...(await page.evaluate(() => ({
      banner:
        document.querySelector(".intsum-banner:not(.intsum-banner-empty)")?.textContent.trim() ?? "",
      evaluationRows: [
        ...document.querySelectorAll(".intsum-section .intsum-table tr td:first-child"),
      ].map((e) => e.textContent.trim()),
      dtg:
        [...document.querySelectorAll(".intsum-header-field")]
          .map((e) => e.textContent.trim())
          .find((t) => t.startsWith("DTG")) ?? "",
    }))),
  };
  // The composed set is the mesh's own work and must be the faceted tier
  // alone. Citations are the provider's choice over that set plus the search
  // branch's external material, which the facets deliberately do not reach
  // (srd016 R4.2; srd011) -- so the citation half of this claim is that no
  // CORPUS citation escapes the tier, not that the provider prefers it. A
  // humint facet plus a strike tasking reliably draws every citation to the
  // strike-specific external document, which is mood, not enforcement.
  check(
    "the faceted turn composes only the faceted tier and no corpus citation escapes it",
    faceted.evalSources.length > 0 &&
      faceted.evalSources.every((id) => id.startsWith("HR-")) &&
      faceted.citedCorpus.every((id) => id.startsWith("HR-")),
    `sources: ${faceted.evalSources.join(", ") || "none"}; corpus cited: ${faceted.citedCorpus.join(", ") || "none (external only)"}`,
  );
  // The release's claim, made visible where a reader sees it: raising the
  // level admits material the lower level never had as a candidate. The
  // comparison is over the sources the two turns composed from -- the mesh's
  // own retrieval, deterministic where a citation is provider sampling.
  const admitted = faceted.evalSources.filter((id) => !facetedLow.evalSources.includes(id));
  check(
    `a faceted turn at ${HIGHEST_CLEARANCE} composes from material the lowest level does not`,
    admitted.length > 0,
    `admitted: ${admitted.join(", ") || "none"} (lowest composed ${facetedLow.evalSources.length}, ${HIGHEST_CLEARANCE} composed ${faceted.evalSources.length})`,
  );
  // The classification tint legend (GH-722): rendered from the composed
  // sources — the mesh's own retrieval — so it must list a level above
  // unclassified exactly when the evaluation column shows one. The tinted
  // marks themselves depend on the provider citing a classified source
  // (mood, not enforcement), so their count is reported, not asserted.
  const classifiedLevels = [
    ...new Set(
      faceted.evalMarkings
        .map((marking) => marking.toLowerCase())
        .filter((marking) => ["restricted", "confidential", "secret"].includes(marking)),
    ),
  ];
  check(
    `the ${HIGHEST_CLEARANCE} turn legends every classified level its sources carry`,
    classifiedLevels.length > 0 &&
      classifiedLevels.every((level) => faceted.legendChips.includes(level.toUpperCase())),
    `evaluation levels: ${classifiedLevels.join(", ") || "none"}; legend: ${faceted.legendChips.join(", ") || "none"}; tinted spans: ${faceted.tintedMarks}`,
  );
  check(
    "an all-unclassified brief renders no marking legend and no tinted span",
    facetedLow.legendChips.length === 0 && facetedLow.tintedMarks === 0,
    `legend: ${facetedLow.legendChips.join(", ") || "none"}; tinted spans: ${facetedLow.tintedMarks}`,
  );
  // srd010 R5.3: the interface must not report what the filter removed. A
  // count is the disclosure, derivable per tasking, and it must not appear
  // anywhere on the page at either level.
  for (const [level, body] of [["unclassified", facetedLow.body], [HIGHEST_CLEARANCE, faceted.body]]) {
    check(
      `the page at ${level} reports nothing about what the filter withheld`,
      !/(\d+\s+(documents?|sources?|results?)\s+(withheld|excluded|hidden|suppressed))/i.test(body) &&
        !/(withheld|suppressed|excluded)\s+\d+/i.test(body),
      "",
    );
  }
  check(
    "the form's banner marks the composed material",
    faceted.banner.length > 0,
    `banner: ${faceted.banner}`,
  );
  check(
    "the source-evaluation table carries the faceted tier's sources",
    faceted.evaluationRows.length > 0 && faceted.evaluationRows.some((s) => s.startsWith("HR-")),
    `rows: ${faceted.evaluationRows.join(", ") || "none"}`,
  );

  check(
    "the header carries the turn's date-time group",
    /\d{6}Z/.test(faceted.dtg),
    faceted.dtg,
  );
  check(
    `the faceted turn reports enforcement at ${HIGHEST_CLEARANCE}`,
    faceted.enforced.some((e) => e.includes(HIGHEST_CLEARANCE)),
    `enforced: ${faceted.enforced.join(" | ")}`,
  );

  // ---- The provenance walk (srd016 R6) ----
  //
  // The walk opens from a corpus citation. The humint-faceted turn above
  // cannot supply one dependably -- its context is fourteen synthetic reports
  // plus the search branch's strike-specific external document, and the
  // provider cites the external document every time -- so the walk runs from
  // an unfaceted turn, whose composed corpus material outnumbers the external
  // document twenty to one and draws the citations (demo:verify measures
  // thirty-nine of thirty-nine resolving to composed chunks).
  await pace();
  const walkTurn = await runFacetedTurn(HIGHEST_CLEARANCE, { facet: false, requireLinks: true });
  check(
    "the citation index offers the walk",
    walkTurn.citationLinks > 0,
    `${walkTurn.citationLinks} record link(s) from an unfaceted turn`,
  );
  if (walkTurn.citationLinks === 0) {
    // No citation to walk from is already a failed check above; failing the
    // walk checks with the reason keeps the report whole instead of crashing
    // on a selector that cannot exist.
    for (const name of [
      "the walk resolves the citation to its chunk",
      "the walk opens the whole parent document around the chunk",
      "the cited passage is highlighted inside the document",
      "the walk lists the document's chunk versions",
      "the walk returns to the form",
    ]) {
      check(name, false, "no citation index entry to open the walk from");
    }
  } else {
  await page.click(".intsum-form .provenance-link");
  await page.waitForSelector(".provenance", { timeout: 30000 });
  // Wait for the walk to finish, not merely to render: the loading state
  // carries its own class so it cannot satisfy this predicate.
  await page.waitForFunction(
    () =>
      document.querySelector(".provenance-loading") === null &&
      (document.querySelector(".provenance-chunk") !== null ||
        document.querySelector(".provenance .intsum-section-empty") !== null ||
        document.querySelector(".provenance-failed") !== null),
    { timeout: 60000 },
  );
  const walk = await page.evaluate(() => ({
    chunkLength: document.querySelector(".provenance-chunk")?.textContent.trim().length ?? 0,
    documentLength: document.querySelector(".provenance-document")?.textContent.trim().length ?? 0,
    highlighted: document.querySelector(".provenance-highlight")?.textContent.trim().length ?? 0,
    versionChips: document.querySelectorAll(".provenance-versions .facet-chip").length,
    failed: document.querySelector(".provenance-failed")?.textContent ?? "",
    markers: (document.querySelector(".provenance-chunk")?.textContent ?? "")
      .match(/\[\d{1,3}\]/g)?.length ?? 0,
    sourceEntries: document.querySelectorAll(".provenance-source").length,
    sourceLinks: document.querySelectorAll(".provenance-sources a").length,
  }));
  check("the walk resolves the citation to its chunk", walk.chunkLength > 0, `${walk.chunkLength} chars${walk.failed ? "; " + walk.failed : ""}`);
  check(
    "the walk opens the whole parent document around the chunk",
    walk.documentLength > walk.chunkLength,
    `document ${walk.documentLength} chars, chunk ${walk.chunkLength}`,
  );
  check(
    "the cited passage is highlighted inside the document",
    walk.highlighted > 0,
    `${walk.highlighted} chars highlighted`,
  );
  check(
    "the walk lists the document's chunk versions",
    walk.versionChips > 0,
    `${walk.versionChips} version chip(s)`,
  );
  // The citation entries the chunk's own markers resolve to (srd017 R10.6).
  // Asserted only where the cited chunk carries markers, because a chunk of a
  // tier that writes none has nothing to resolve and renders no block.
  if (walk.markers > 0) {
    check(
      "the cited chunk names the sources its markers cite",
      walk.sourceEntries > 0,
      `${walk.markers} marker(s), ${walk.sourceEntries} source entry/entries`,
    );
    // The tier obscures the addresses it considers hostile to follow, so the
    // panel shows them as written rather than turning them into links.
    check(
      "the cited sources are shown as written rather than linked",
      walk.sourceLinks === 0,
      `${walk.sourceLinks} anchor(s) under the sources block`,
    );
  }

  const backToForm = await page.$(".provenance-back");
  await backToForm.click();
  await page.waitForSelector(".intsum-form", { timeout: 15000 });
  check("the walk returns to the form", true, "");
  }

  // ---- Withholding matches the boundary (srd016 R6.5, srd017 R8.7) ----
  //
  // A document the secret turn composed from and the lowest turn did not is
  // read through the browser at both levels, by its envelope record. The
  // lower read answers the same 200 with nothing in it -- absence rather than
  // refusal (srd016 R5.7).
  const probeRecord = admitted[0] ? `${admitted[0]}#envelope` : undefined;
  if (probeRecord) {
    const reads = await page.evaluate(async (recordID) => {
      const read = async (clearance) => {
        const res = await fetch("/api/v1/documents", {
          method: "POST",
          headers: { "content-type": "application/json", "X-Analyst-Clearance": clearance },
          body: JSON.stringify({ record_ids: [recordID] }),
        });
        const body = await res.json();
        const served = (body.records ?? []).flatMap(
          (r) => r.result?.structured_output?.mapped?.ids ?? [],
        );
        return { status: res.status, served };
      };
      return { low: await read("unclassified"), high: await read("secret") };
    }, probeRecord);
    check(
      "a document above the viewer's level is answered as absent rather than refused",
      reads.low.status === 200 && reads.low.served.length === 0,
      `status ${reads.low.status}, served: ${reads.low.served.join(", ") || "none"}`,
    );
    check(
      "the same identifier serves at the level that cleared it",
      reads.high.status === 200 && reads.high.served.includes(probeRecord),
      `status ${reads.high.status}, served: ${reads.high.served.join(", ") || "none"}`,
    );
  } else {
    check("a document above the viewer's level is answered as absent rather than refused", false,
      "no record was admitted only at the higher level, so the withholding probe has nothing to read");
  }

  // ---- Corpus panel: an opened chunk highlights in its parent (GH-602) ----
  await page.goto(`${BASE}/ui/corpus`, { waitUntil: "networkidle2", timeout: 60000 });
  await page.waitForSelector(".corpus-doc-toggle", { timeout: 30000 });
  await page.click(".corpus-doc-toggle");
  await page.waitForSelector(".corpus-chunk-unravel", { timeout: 15000 });
  await page.click(".corpus-chunk-unravel");
  await page.waitForSelector(".corpus-chunk-toggle", { timeout: 30000 });
  await page.click(".corpus-chunk-toggle");
  await page.waitForSelector(".corpus-chunk-highlight", { timeout: 10000 }).catch(() => {});
  const corpusHighlight = await page.evaluate(() => {
    const mark = document.querySelector(".corpus-chunk-highlight");
    return {
      chars: mark ? mark.textContent.length : 0,
      inDocument: mark ? mark.closest(".corpus-text") !== null : false,
    };
  });
  check(
    "opening a chunk highlights its passage in the parent document",
    corpusHighlight.chars > 50 && corpusHighlight.inDocument,
    `${corpusHighlight.chars} chars highlighted`,
  );

  // ---- Every route renders ----
  await page.goto(`${BASE}/ui/provisioning`, { waitUntil: "networkidle2", timeout: 60000 });
  const provisioningText = await page.$eval("body", (b) => b.innerText);
  check("the provisioning panel renders", /provisioning/i.test(provisioningText), "");

  // ---- The browser never reaches the store (srd016 R5.2) ----
  check(
    "every request goes to the mesh boundary and none to the vector store",
    !requestedURLs.some((u) => u.includes("/api/v2/tenants/")),
    `${requestedURLs.length} requests inspected`,
  );

  // ---- The fleet observer (GH-439, GH-737) ----
  //
  // The observer serves the fleet bundle compiled into agent-core, so what this
  // mesh asserts is its own: the bundle is served at the observer's own origin,
  // it names the agents this release deploys, each card carries the state and
  // the declared surface it read through the monitor proxy, and the topology
  // draws the edges the traces observed. How the kit lays a card out is the
  // kit's own claim, covered by its suite upstream (srd004 R9).
  await page.goto(`${OBSERVER_BASE}/ui/`, { waitUntil: "networkidle2", timeout: 60000 });
  await page.waitForFunction(
    () =>
      [...document.querySelectorAll(".card")].some(
        (card) => card.querySelector(".agent-name")?.textContent === "chatbot",
      ),
    { timeout: 60000 },
  );
  // The topology and the request walk derive from the collector's recent
  // traces, fetched one by one; on object storage each fetch reads the bucket,
  // so the figure settles over tens of seconds rather than on first paint.
  await page
    .waitForFunction(
      () => {
        const chatbot = [...document.querySelectorAll(".card")].find(
          (card) => card.querySelector(".agent-name")?.textContent === "chatbot",
        );
        return (
          document.body.textContent.includes("calls observed") &&
          (chatbot?.querySelectorAll(".walk-step").length ?? 0) > 0
        );
      },
      { timeout: 120000, polling: 2000 },
    )
    .catch(() => {});
  const fleet = await page.evaluate(() => {
    const cards = [...document.querySelectorAll(".card")];
    const chatbot = cards.find((card) => card.querySelector(".agent-name")?.textContent === "chatbot");
    return {
      agents: cards.map((card) => card.querySelector(".agent-name")?.textContent ?? ""),
      state: chatbot?.querySelector(".agent-state")?.textContent?.trim() ?? "",
      pod: chatbot?.querySelector(".agent-pod")?.textContent?.trim() ?? "",
      tools: chatbot?.querySelectorAll(".tool-tag").length ?? 0,
      walkSteps: chatbot?.querySelectorAll(".walk-step").length ?? 0,
      topology: document.body.textContent.includes("calls observed"),
    };
  });
  check(
    "the observer serves the embedded bundle and names this release's agents",
    fleet.agents.includes("chatbot") && fleet.agents.includes("rag0") && fleet.agents.includes("observer"),
    `agents: ${fleet.agents.join(", ")}`,
  );
  check(
    "the chatbot card reports the state and pod it read through the proxy",
    fleet.state.length > 0 && fleet.pod.startsWith("cohere-demo-chatbot-mesh-chatbot"),
    `state: ${fleet.state}; pod: ${fleet.pod}`,
  );
  check(
    "the card carries the declared surface the monitor served",
    fleet.tools > 0 && fleet.walkSteps > 0,
    `tools: ${fleet.tools}, walk steps: ${fleet.walkSteps}`,
  );
  check(
    "the topology draws the edges the traces observed",
    fleet.topology,
    `topology text present: ${fleet.topology}`,
  );


  // ---- Whole-run hygiene ----
  //
  // The absent-agent probe is expected and bounded: the shipped list is a
  // superset of the deployment, so the SPA asks once per page load to find out.
  // Zero is not reachable without serving the deployed list to the browser.
  // Everything else that fails is a real failure.
  const absentProbes = failedRequests.filter((r) => absentProbePattern.test(r));
  const otherFailures = failedRequests.filter(
    (r) => !r.includes("favicon") && !absentProbePattern.test(r),
  );
  check(
    "no failed requests other than the bounded absent-agent probe",
    otherFailures.length === 0,
    otherFailures.slice(0, 5).join("; "),
  );
  // Each visit to the Metrics panel probes the absent agent once at
  // discovery, and the run now visits it twice -- the panel sweep and the
  // request-machine walk (GH-413). The bound tracks visits; the looping this
  // catches shows up as dozens.
  check(
    "the absent-agent probe stays at discovery scale rather than looping",
    absentProbes.length <= 4,
    `${absentProbes.length} probe(s) across the run`,
  );
  // The browser logs a console error for every failed response, so the probe's
  // own lines are the only ones tolerated here; the counts above bound them.
  const otherConsoleErrors = consoleErrors.filter((e) => !/Failed to load resource/.test(e));
  check(
    "no console errors beyond the failed-response lines the probe produces",
    otherConsoleErrors.length === 0,
    otherConsoleErrors.slice(0, 3).join(" | "),
  );

  if (SCREENSHOT) await page.screenshot({ path: SCREENSHOT });
} finally {
  await browser.close();
}

const failed = results.filter((r) => !r.pass);
console.log(`\n${results.length - failed.length}/${results.length} browser checks passed`);
process.exit(failed.length === 0 ? 0 : 1);
