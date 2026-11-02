// Slash-command dispatch for the chat window (GH-449). The window's job is
// producing a document, so intent is dispatched, not inferred: a leading
// /command activates a specific declared workflow, and the two defaulting
// rules cover plain text -- an empty window researches, a window holding a
// document revises it. No model ever routes intent; parsing here is the
// whole router, and each command maps to one REST endpoint and one declared
// machine the fleet observer shows as its own workflow.

export type Command = "help" | "audit" | "refine" | "make-brief" | "search" | "summarize" | "revise" | "export" | "harvest" | "eval" | "translate" | "clearance";

export interface ParsedInput {
  command: Command;
  argument: string;
}

export interface ParseError {
  error: string;
}

// The palette the window renders and the parser accepts, with the Cohere
// models each workflow features -- the demo's cast list.
export const COMMANDS: Array<{
  command: Command;
  usage: string;
  models: string;
  describes: string;
}> = [
  {
    command: "make-brief",
    usage: "/make-brief <tasking>",
    models: "Embed · Rerank · Command",
    describes: "Full brief: retrieve, rerank, compose a cited INTSUM. Default on an empty window.",
  },
  {
    command: "revise",
    usage: "/revise <instruction>",
    models: "Embed · Rerank · Command",
    describes: "Change the brief on screen: retrieve what the instruction asks for and regenerate. Default when a brief is showing.",
  },
  {
    command: "search",
    usage: "/search <query>",
    models: "Embed",
    describes: "Retrieval alone: the corpus chunks with provenance, no brief.",
  },
  {
    command: "summarize",
    usage: "/summarize",
    models: "Command",
    describes: "Summarize the document on screen. No retrieval.",
  },
  {
    command: "harvest",
    usage: "/harvest <query>",
    models: "Command · Embed",
    describes: "Search the web and ingest the results into the scratch corpus; later research turns retrieve them at unclassified.",
  },
  {
    command: "export",
    usage: "/export",
    models: "—",
    describes: "Render the brief on screen as a PDF.",
  },
  {
    command: "help",
    usage: "/help",
    models: "—",
    describes: "Show the workflow map and command vocabulary again.",
  },
  {
    command: "audit",
    usage: "/audit [sentences]",
    models: "Command",
    describes: "Judge each sentence of the brief against the chunks it cites, scored 1-10, weakest first in the report table.",
  },
  {
    command: "refine",
    usage: "/refine [passes]",
    models: "Command",
    describes: "Audit the brief and revise against the findings, up to N passes; a clean audit stops the loop.",
  },
  {
    command: "clearance",
    usage: "/clearance <level>",
    models: "—",
    describes: "Set the analyst clearance for the next tasking: unclassified, restricted, confidential, or secret.",
  },
  {
    command: "translate",
    usage: "/translate <language>",
    models: "Command",
    describes: "Render the brief in French, German, Polish, or Romanian — a rendition of the English record; citations and gradings stay.",
  },
  {
    command: "eval",
    usage: "/eval [count]",
    models: "Embed · Rerank · Command",
    describes: "Run the retrieval-recall evaluation over the generated reference set and report the numbers here.",
  },
];

const KNOWN = new Set(COMMANDS.map((entry) => entry.command));

export function isParseError(parsed: ParsedInput | ParseError): parsed is ParseError {
  return "error" in parsed;
}

// parseCommand routes one submitted input. Explicit commands always win;
// plain text takes the defaulting rules. Unknown commands come back as an
// error naming the vocabulary rather than silently researching, because a
// typo that runs the wrong workflow costs a provider call.
export function parseCommand(input: string, hasDocument: boolean): ParsedInput | ParseError {
  const trimmed = input.trim();
  if (!trimmed.startsWith("/")) {
    return { command: hasDocument ? "revise" : "make-brief", argument: trimmed };
  }
  const space = trimmed.indexOf(" ");
  const name = (space === -1 ? trimmed.slice(1) : trimmed.slice(1, space)).toLowerCase();
  const argument = space === -1 ? "" : trimmed.slice(space + 1).trim();
  if (!KNOWN.has(name as Command)) {
    return {
      error: `unknown command /${name} — the vocabulary is ${COMMANDS.map((c) => "/" + c.command).join(", ")}`,
    };
  }
  const command = name as Command;
  if ((command === "make-brief" || command === "search" || command === "harvest") && argument === "") {
    return { error: `/${command} needs an argument: ${COMMANDS.find((c) => c.command === command)?.usage}` };
  }
  if (command === "eval" && argument !== "" && !/^\d+$/.test(argument)) {
    return { error: "/eval takes an optional tasking count: /eval [count]" };
  }
  if (command === "revise" && argument === "") {
    return { error: "/revise needs the change you want: /revise <instruction>" };
  }
  if (command === "revise" && !hasDocument) {
    return { error: "/revise needs a brief on screen — run /make-brief first" };
  }
  if (command === "audit" && argument !== "" && !/^\d+$/.test(argument)) {
    return { error: "/audit takes an optional sentence count: /audit [sentences]" };
  }
  if (command === "refine" && !hasDocument) {
    return { error: "/refine needs a brief on screen — run /make-brief first" };
  }
  if (command === "refine" && argument !== "" && !/^[1-3]$/.test(argument)) {
    return { error: "/refine takes an optional pass count from 1 to 3" };
  }
  if (command === "audit" && !hasDocument) {
    return { error: "/audit needs a brief on screen — run /make-brief first" };
  }
  if (command === "clearance" && !/^(unclassified|restricted|confidential|secret)$/i.test(argument.trim())) {
    return { error: "/clearance takes a level: unclassified, restricted, confidential, or secret" };
  }
  if (command === "translate" && argument === "") {
    return { error: "/translate needs a language: /translate <language>" };
  }
  if (command === "translate" && !hasDocument) {
    return { error: "/translate needs a brief on screen — run /make-brief first" };
  }
  if (command === "summarize" && !hasDocument) {
    return { error: "/summarize needs a document on screen — run /make-brief or /search first" };
  }
  if (command === "export" && !hasDocument) {
    return { error: "/export needs a brief on screen — run /make-brief first" };
  }
  return { command, argument };
}
