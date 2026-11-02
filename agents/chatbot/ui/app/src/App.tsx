import { useEffect, useState } from "react";
import config from "virtual:ui-config";
import { TraceStoryOverlay, usePanelLocation, type PanelRoute } from "@declarative-agents/ui-kit";
import { panels } from "./panels";
import { storyChats } from "./storyChats";
import { traceOptions } from "./traceOptions";
import { TurnProvider } from "./turns";
import {
  clearChats,
  deleteChat,
  listChats,
  openChat,
  subscribeList,
  type StoredChatSummary,
} from "./chatHistory";

// The shell (srd002 R5, srd004 R5): ui.yaml declares the panels and this file
// mounts them from the registry. The active panel is derived from the URL
// rather than held in component state, so every panel ui.yaml declares is
// linkable and survives a reload (GH-723), and the path helpers are the kit's
// so the URL scheme is the one every shell in the platform uses (R5.3).
//
// The kit's AppShell is not mounted here: the sidebar carries this
// application's chat history, its Traces action, and the fleet link, and the
// shell has no slot for them (upstream GH-2286). The composition input, the
// registry contract, the routing, and the panel frame are the kit's all the
// same.

const routes: PanelRoute[] = (config.panels ?? []).map((panel) => ({
  id: panel.id,
  path: panel.route,
  label: panel.label ?? panel.id,
  hidden: panel.hidden,
  group: panel.sidebar_group,
}));

const routing = { routes, defaultPanel: routes[0]?.id ?? "chat" };

const monitoredAgents = config.monitored_agents ?? [];
const traceBackend = config.trace_backend?.name;

// The fleet observer is another service at its own origin, so it is a link
// rather than a panel: ui.yaml declares panels this app renders, and the
// observer renders itself. Its host is the one app:up pins on the platform
// ingress (ingress.observerHost), protocol-relative so the link follows however
// the page was reached.
const EXTERNAL_LINKS = [{ href: "//observer.cohere-demo.localhost/", label: "Fleet" }];

function panelFor(active: string): React.ReactNode {
  const Panel = panels[active];
  if (!Panel) return null;
  return <Panel monitoredAgents={monitoredAgents} traceBackend={traceBackend} />;
}

// The previous-chats list under the Chat nav item (GH-509): titles from the
// client-side history, newest first, with a new-chat action. Clicking one
// restores that conversation in the panel; the × beside it deletes the stored
// chat without opening it (GH-588).
function ChatHistoryList({ onOpen }: { onOpen: (id: string | undefined) => void }) {
  const [chats, setChats] = useState<StoredChatSummary[]>(() => listChats());
  useEffect(() => subscribeList(() => setChats(listChats())), []);
  return (
    <div className="chat-history">
      <button type="button" className="chat-history-new" onClick={() => onOpen(undefined)}>
        + new chat
      </button>
      {chats.map((chat) => (
        <div className="chat-history-row" key={chat.id}>
          <button
            type="button"
            className="chat-history-item"
            title={chat.title}
            onClick={() => onOpen(chat.id)}
          >
            {chat.title}
          </button>
          <button
            type="button"
            className="chat-history-delete"
            title="Delete this chat from the stored history"
            aria-label={`Delete chat: ${chat.title}`}
            onClick={() => deleteChat(chat.id)}
          >
            ×
          </button>
        </div>
      ))}
    </div>
  );
}

export default function App() {
  // The kit tracks the URL and the active panel, including back and forward.
  const { active, href, navigate: navigateTo } = usePanelLocation(routing);
  // The sidebar Traces entry (GH-640): opens the story overlay over whatever
  // panel is active. It is a button, not a panel route -- the hidden
  // /observability panel stays declared for the deep link.
  const [tracesOpen, setTracesOpen] = useState(false);

  // Ctrl/Cmd-Shift-R is the demo reset (GH-588): the stored history is cleared
  // synchronously here, and the browser's own hard reload then brings the app
  // back empty. No preventDefault — the reload IS the second half of the reset.
  // The beacon (GH-648) asks the mesh to drop harvested web rows — it survives
  // the unload, and a failed send never blocks the reset.
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.shiftKey && event.key.toLowerCase() === "r") {
        try {
          navigator.sendBeacon(
            "/api/v1/harvest/purge",
            new Blob(['{"reason": "ui-reset"}'], { type: "application/json" }),
          );
        } catch {
          // The endpoint may be absent on a tier without harvest; reset anyway.
        }
        clearChats();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);


  return (
    <TurnProvider>
      <div className="shell">
        <nav className="sidebar">
          <div className="sidebar-title">{config.sidebar?.title ?? config.title}</div>
          {routes.filter((item) => !item.hidden).map((item) => (
            <div key={item.id}>
              <a
                href={href(item.id)}
                className={`nav-item${active === item.id ? " nav-item-active" : ""}`}
                onClick={(event) => navigateTo(event, item.id)}
              >
                <span>{item.label}</span>
              </a>
              {item.id === "chat" && (
                <>
                  <ChatHistoryList
                    onOpen={(id) => {
                      openChat(id);
                      if (active !== "chat") window.history.pushState({}, "", href("chat"));
                    }}
                  />
                  <button
                    type="button"
                    className="nav-item"
                    onClick={() => setTracesOpen(true)}
                  >
                    <span>Traces</span>
                  </button>
                </>
              )}
            </div>
          ))}
          {EXTERNAL_LINKS.map((link) => (
            // A separate service at its own origin: a plain anchor in a new
            // tab, so the turn on this page survives the visit.
            <a
              key={link.href}
              href={link.href}
              className="nav-item nav-item-external"
              target="_blank"
              rel="noreferrer"
            >
              <span>{link.label}</span>
              <span className="nav-external-mark" aria-hidden="true">↗</span>
            </a>
          ))}
        </nav>
        <main className="content">{panelFor(active)}</main>
        {tracesOpen && (
          <TraceStoryOverlay
            {...traceOptions}
            chats={storyChats()}
            backend={traceBackend}
            onClose={() => setTracesOpen(false)}
          />
        )}
      </div>
    </TurnProvider>
  );
}
