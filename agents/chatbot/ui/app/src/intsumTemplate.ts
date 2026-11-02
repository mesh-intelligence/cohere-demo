// The ministry INTSUM template as the app reads it (srd016 R2).
//
// The declaration lives in agents/chatbot/ui/intsum-template.yaml. This table
// mirrors it, and a Go test cross-checks the two in both directions, the way
// TestUIDeclaredRoutesAreImplemented cross-checks the route table against
// ui.yaml. The form renders from this table rather than from a section list
// written into a component, so there is one place the structure is stated and
// one check that keeps the copies in step (R2.4).

export const INTSUM_TEMPLATE_VERSION = "1";

// Where a section's content comes from. The two fail differently and the form
// says which happened: a composed section is absent because the provider wrote
// no such heading, a derived section is absent because the turn composed no
// sources (R3.2).
export type SectionSource = "composed_markdown" | "derived";

export interface TemplateColumn {
  id: string;
  label: string;
}

export interface TemplateSection {
  id: string;
  label: string;
  order: number;
  source: SectionSource;
  required: boolean;
  // Declared for a derived section: which part of the response assembles it.
  derivedFrom?: string;
  // Declared for a composed section: the headings a provider may write it
  // under. A heading outside every section's list is reported as unmatched
  // rather than guessed into a section (srd012 R1.3).
  headings?: string[];
  fields?: TemplateColumn[];
  columns?: TemplateColumn[];
}

export const INTSUM_SECTIONS: TemplateSection[] = [
  {
    id: "classification-banner",
    label: "Classification",
    order: 0,
    source: "derived",
    derivedFrom: "composed_source_clearance",
    required: true,
  },
  {
    id: "header",
    label: "Header",
    order: 1,
    source: "derived",
    derivedFrom: "turn_context",
    required: true,
    fields: [
      { id: "date_time_group", label: "DTG" },
      { id: "originator", label: "Originator" },
      { id: "tasking", label: "Tasking" },
    ],
  },
  {
    id: "bluf",
    label: "Bottom Line Up Front",
    order: 2,
    source: "composed_markdown",
    required: true,
    headings: ["Bottom Line Up Front", "BLUF", "Key Judgements", "Key Judgments"],
  },
  {
    id: "threat-activity",
    label: "Threat Activity",
    order: 3,
    source: "composed_markdown",
    required: true,
    headings: ["Threat Activity", "Assessment", "Supporting Reporting"],
  },
  {
    id: "source-evaluation",
    label: "Source Evaluation",
    order: 4,
    source: "derived",
    derivedFrom: "composed_source_admiralty",
    required: true,
    columns: [
      { id: "source", label: "Source" },
      { id: "classification", label: "Classification" },
      { id: "reliability", label: "Reliability" },
      { id: "credibility", label: "Credibility" },
    ],
  },
  {
    id: "gaps",
    label: "Gaps",
    order: 5,
    source: "composed_markdown",
    required: true,
    headings: ["Gaps", "Intelligence Gaps"],
  },
  {
    id: "outlook",
    label: "Outlook",
    order: 6,
    source: "composed_markdown",
    required: false,
    headings: ["Outlook", "Assessment Outlook"],
  },
  {
    id: "citation-index",
    label: "Citation Index",
    order: 7,
    source: "derived",
    derivedFrom: "response_citations",
    required: true,
    columns: [
      { id: "record", label: "Record" },
      { id: "assertions", label: "Assertions" },
    ],
  },
];

// sectionForHeading resolves a markdown heading to the section that declares
// it, matched case-insensitively on trimmed text, in the sections the caller
// is filling -- the runtime template where one is configured (GH-929). A
// heading no section declares returns undefined, which the contract records as
// unmatched rather than assigning to whichever section came last.
export function sectionForHeading(
  heading: string,
  sections: TemplateSection[] = INTSUM_SECTIONS,
): TemplateSection | undefined {
  const wanted = heading.trim().toLowerCase();
  return sections.find((section) =>
    (section.headings ?? []).some((candidate) => candidate.toLowerCase() === wanted),
  );
}
