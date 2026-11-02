import type { ComponentType } from "react";
import ChatPanel from "./ChatPanel";
import CorpusPanel from "./CorpusPanel";
import ObservabilityPanel from "./ObservabilityPanel";
import ProvisioningPanel from "./ProvisioningPanel";
import type { PanelProps } from "@declarative-agents/ui-kit";

// The application's panel registry (srd004 R5): one entry per local panel
// ui.yaml declares, keyed by its panel id. ui.yaml is the route table and this
// is the mount table, so adding a panel is one entry in each;
// TestChatbotUIPanelsMatchTheRegistry fails when the two disagree.
export const panels: Record<string, ComponentType<PanelProps>> = {
  chat: ChatPanel as ComponentType<PanelProps>,
  observability: ObservabilityPanel,
  corpus: CorpusPanel as ComponentType<PanelProps>,
  provisioning: ProvisioningPanel as ComponentType<PanelProps>,
};
