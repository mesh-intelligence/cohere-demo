// Client-side chat history (GH-509): every conversation autosaves to
// localStorage so a reload restores it, and the sidebar lists previous chats.
// Zero server bytes -- the mesh never sees this state, and a browser that
// blocks storage degrades to the old ephemeral behavior.

export interface StoredChatSummary {
  id: string;
  title: string;
  startedAt: number;
}

export interface StoredChat extends StoredChatSummary {
  // The panel's own message and answer shapes, stored as plain JSON. Typed by
  // the caller on load; this module never inspects them.
  messages: unknown[];
  latestAnswer?: unknown;
  lastTasking?: string;
  latestContext?: unknown;
}

const STORE_KEY = "chatbot-chat-history-v1";
const MAX_CHATS = 20;

type Listener = () => void;
const listListeners = new Set<Listener>();
type OpenListener = (chat: StoredChat | undefined) => void;
const openListeners = new Set<OpenListener>();

function readStore(): StoredChat[] {
  try {
    const raw = localStorage.getItem(STORE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as StoredChat[];
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function writeStore(chats: StoredChat[]): void {
  const bounded = [...chats].sort((a, b) => b.startedAt - a.startedAt).slice(0, MAX_CHATS);
  try {
    localStorage.setItem(STORE_KEY, JSON.stringify(bounded));
  } catch {
    // Quota: drop the older half and try once more; failing that, live without.
    try {
      localStorage.setItem(STORE_KEY, JSON.stringify(bounded.slice(0, Math.ceil(bounded.length / 2))));
    } catch {
      return;
    }
  }
  listListeners.forEach((listener) => listener());
}

export function newChatId(): string {
  return `chat-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}

export function listChats(): StoredChatSummary[] {
  return readStore().map(({ id, title, startedAt }) => ({ id, title, startedAt }));
}

export function loadChat(id: string): StoredChat | undefined {
  return readStore().find((chat) => chat.id === id);
}

export function saveChat(chat: StoredChat): void {
  const rest = readStore().filter((stored) => stored.id !== chat.id);
  writeStore([chat, ...rest]);
}

export function deleteChat(id: string): void {
  writeStore(readStore().filter((chat) => chat.id !== id));
}

// The demo reset (GH-588): drop every stored chat. Bound to Ctrl/Cmd-Shift-R
// in App, where it runs before the browser's own hard reload lands, so the
// shortcut brings the app back to a clean slate.
export function clearChats(): void {
  try {
    localStorage.removeItem(STORE_KEY);
  } catch {
    // A browser that blocks storage kept nothing to clear.
  }
  listListeners.forEach((listener) => listener());
}

// The sidebar (App) and the panel (ChatPanel) are siblings; these two hooks
// are the seam between them. subscribeList re-renders the sidebar when a chat
// saves; openChat asks the panel to load one (undefined = start fresh).
export function subscribeList(listener: Listener): () => void {
  listListeners.add(listener);
  return () => listListeners.delete(listener);
}

export function subscribeOpen(listener: OpenListener): () => void {
  openListeners.add(listener);
  return () => openListeners.delete(listener);
}

export function openChat(id: string | undefined): void {
  const chat = id ? loadChat(id) : undefined;
  openListeners.forEach((listener) => listener(chat));
}
