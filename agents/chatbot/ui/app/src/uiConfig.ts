// Runtime interface vocabulary (GH-929). The bundle compiles in the DefTech
// vocabulary -- the clearance labels, the Admiralty wording, the INTSUM
// template -- and a case that is not defense showed defense words for its own
// levels and a form that did not match its memo. This module lets a deployment
// rebind the DISPLAYED vocabulary at runtime without touching the wire: level
// values stay the declared set (srd018 R3.5), the levels' order and gating
// stay the runtime's, and only what the reader sees changes.
//
// The config is the ui route's own declared static_assets.config, which the
// runtime serves as ui-config.json beneath the mount (srd029 R5.10), and the
// chart renders from chatbot.ui values -- declaration data, no rebuild. No
// config, a 404, a fallback HTML page, or a malformed document all mean the
// compiled defaults: a kind dev loop and the DefTech profile ship unchanged.

import {
  INTSUM_SECTIONS,
  INTSUM_TEMPLATE_VERSION,
  type TemplateSection,
} from "./intsumTemplate";

export interface UIRuntimeConfig {
  // Display label per wire level value; a level without an entry shows its
  // wire value, so a partial map is safe.
  levelLabels?: Record<string, string>;
  // The label on the chat panel's level selector ("analyst clearance").
  clearanceSelectLabel?: string;
  // The label on the corpus panel's level selector ("clearance").
  corpusLevelLabel?: string;
  // The label on the reader selector ("reader clearance").
  readerSelectLabel?: string;
  // The reliability-floor control's label ("source reliability (Admiralty)").
  reliabilityFloorLabel?: string;
  // The reliability-floor control's tooltip.
  reliabilityFloorTitle?: string;
  // What the report form is called ("INTSUM"), used by the form chrome.
  formName?: string;
  // The report template the form renders. The same schema the compiled
  // intsum-template.yaml declares; a document that fails the shape check is
  // rejected whole rather than half-applied.
  templateVersion?: string;
  template?: TemplateSection[];
}

let runtime: UIRuntimeConfig = {};

export const UI_CONFIG_PATH = "/ui/ui-config.json";

// loadUIConfig fetches the deployment's vocabulary once at startup. Absent or
// unreadable config is the compiled default, not an error: the route exists
// only where the chart rendered it.
export async function loadUIConfig(): Promise<void> {
  try {
    const res = await fetch(UI_CONFIG_PATH, { headers: { Accept: "application/json" } });
    if (!res.ok) return;
    const body: unknown = await res.json();
    runtime = validateUIConfig(body);
  } catch {
    runtime = {};
  }
}

// setUIConfigForTest replaces the loaded config; tests use it instead of a
// fetch stub so the accessors stay synchronous.
export function setUIConfigForTest(config: UIRuntimeConfig): void {
  runtime = config;
}

// validateUIConfig keeps a malformed document from half-applying: scalar
// fields are taken when they are strings, and the template is taken only when
// every section carries the fields the form render needs.
export function validateUIConfig(body: unknown): UIRuntimeConfig {
  if (typeof body !== "object" || body === null) return {};
  const raw = body as Record<string, unknown>;
  const config: UIRuntimeConfig = {};
  if (typeof raw.levelLabels === "object" && raw.levelLabels !== null) {
    const labels: Record<string, string> = {};
    for (const [level, label] of Object.entries(raw.levelLabels as Record<string, unknown>)) {
      if (typeof label === "string" && label !== "") labels[level] = label;
    }
    if (Object.keys(labels).length > 0) config.levelLabels = labels;
  }
  for (const field of [
    "clearanceSelectLabel",
    "corpusLevelLabel",
    "readerSelectLabel",
    "reliabilityFloorLabel",
    "reliabilityFloorTitle",
    "formName",
    "templateVersion",
  ] as const) {
    const value = raw[field];
    if (typeof value === "string" && value !== "") config[field] = value;
  }
  if (Array.isArray(raw.template) && raw.template.length > 0) {
    const sections: TemplateSection[] = [];
    let valid = true;
    for (const entry of raw.template as unknown[]) {
      const section = validateTemplateSection(entry);
      if (!section) {
        valid = false;
        break;
      }
      sections.push(section);
    }
    if (valid) config.template = sections;
  }
  return config;
}

function validateTemplateSection(entry: unknown): TemplateSection | undefined {
  if (typeof entry !== "object" || entry === null) return undefined;
  const raw = entry as Record<string, unknown>;
  if (typeof raw.id !== "string" || raw.id === "") return undefined;
  if (typeof raw.label !== "string" || raw.label === "") return undefined;
  if (typeof raw.order !== "number") return undefined;
  if (raw.source !== "composed_markdown" && raw.source !== "derived") return undefined;
  if (typeof raw.required !== "boolean") return undefined;
  const section: TemplateSection = {
    id: raw.id,
    label: raw.label,
    order: raw.order,
    source: raw.source,
    required: raw.required,
  };
  if (typeof raw.derivedFrom === "string") section.derivedFrom = raw.derivedFrom;
  if (Array.isArray(raw.headings) && raw.headings.every((h) => typeof h === "string")) {
    section.headings = raw.headings as string[];
  }
  for (const key of ["fields", "columns"] as const) {
    const columns = raw[key];
    if (!Array.isArray(columns)) continue;
    const parsed = [];
    for (const column of columns as unknown[]) {
      if (
        typeof column !== "object" ||
        column === null ||
        typeof (column as Record<string, unknown>).id !== "string" ||
        typeof (column as Record<string, unknown>).label !== "string"
      ) {
        return undefined;
      }
      parsed.push({
        id: (column as Record<string, string>).id,
        label: (column as Record<string, string>).label,
      });
    }
    section[key] = parsed;
  }
  return section;
}

// levelLabel is the display name for one wire level value.
export function levelLabel(level: string): string {
  return runtime.levelLabels?.[level] ?? level;
}

export function clearanceSelectLabel(): string {
  return runtime.clearanceSelectLabel ?? "analyst clearance";
}

export function corpusLevelLabel(): string {
  return runtime.corpusLevelLabel ?? "clearance";
}

export function readerSelectLabel(): string {
  return runtime.readerSelectLabel ?? "reader clearance";
}

export function reliabilityFloorLabel(): string {
  return runtime.reliabilityFloorLabel ?? "source reliability (Admiralty)";
}

export function reliabilityFloorTitle(): string {
  return (
    runtime.reliabilityFloorTitle ??
    "Retrieval admits only sources graded at or above this Admiralty reliability floor"
  );
}

export function formName(): string {
  return runtime.formName ?? "INTSUM";
}

// templateSections is what the form fills: the deployment's declared template,
// or the compiled INTSUM one.
export function templateSections(): TemplateSection[] {
  return runtime.template ?? INTSUM_SECTIONS;
}

export function templateVersion(): string {
  if (runtime.template) return runtime.templateVersion ?? "runtime";
  return INTSUM_TEMPLATE_VERSION;
}
