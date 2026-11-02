import type { Milestones } from "@declarative-agents/ui-kit";

// The lines shown while a turn composes (GH-380). The kit streams the serving
// agent's transitions and renders the feed; which words are worth a line is
// this application's claim about its own machine, so the map stays here.
//
// The map names milestone words, not states: a word is what the machine
// dispatched, so a label here is a claim about work that ran. Everything
// unmapped is silence, which is what keeps the feed readable; a turn whose
// shape changes shows fewer highlights rather than wrong ones.

// One label per milestone word. Words that repeat across branches (the three
// rerank passes) carry their branch in the label, because "reranking" twice
// with no distinction reads as a stutter rather than as two passes.
export const CHAT_MILESTONES: Milestones = {
  embed_query_cohere: "embedding the tasking",
  embed_query: "embedding the tasking",
  declare_rag_topology: "reading the retrieval topology",
  select_sources: "choosing which sources to query",
  rag_query: "querying the RAG units",
  rag_query_faceted: "querying the RAG units under the facets",
  osint_search: "searching the external OSINT branch",
  cohere_rerank_rag: "reranking the corpus results (Cohere)",
  cohere_rerank_search: "reranking the search results (Cohere)",
  cohere_rerank_fused: "fusing and reranking both branches (Cohere)",
  compose_documents: "assembling the composition set",
  compose_documents_augmented: "assembling the composition set with search results",
  compose_documents_osint_only: "assembling the composition set from search results",
  invoke_cohere_command: "composing the brief (Cohere Command)",
  invoke_llm_fast: "composing the answer (local model)",
  invoke_llm_deep: "composing the answer (local model)",
  capture_brief: "capturing the composed brief",
  parse_citations: "resolving the citations",
  check_conformance: "checking the brief against the ministry template",
  partition_reader_floor: "applying the reader's release floor",
  compose_response: "rendering the response",
};

