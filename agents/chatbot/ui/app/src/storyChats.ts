import type { TraceChatTurns } from "@declarative-agents/ui-kit";
import { listChats, loadChat } from "./chatHistory";

// The chats the kit's trace story overlay draws (GH-517). History is this
// application's own store, so the kit takes the list rather than reading it:
// every stored chat that carries traces, newest first. The overlay pins the
// launching chat itself from its currentChatId prop; without one (the sidebar
// entry, GH-640) the newest leads.
export function storyChats(): TraceChatTurns[] {
  const index: TraceChatTurns[] = [];
  for (const summary of listChats()) {
    const stored = loadChat(summary.id);
    if (!stored) continue;
    const messages = (stored.messages as Array<{ role: string; content: string; traceId?: string }>) ?? [];
    const turns: TraceChatTurns["turns"] = [];
    let lastUser = "";
    for (const message of messages) {
      if (message.role === "user") lastUser = message.content;
      if (message.traceId) {
        turns.push({ traceId: message.traceId, label: lastUser.slice(0, 48) || `turn ${turns.length + 1}` });
      }
    }
    if (turns.length > 0) index.push({ chatId: summary.id, title: summary.title.slice(0, 48), turns });
  }
  return index;
}
