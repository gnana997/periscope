// ExecSessionsCtx primitive + value type — lives in its own file so
// React Refresh can hot-reload <ExecSessionsProvider> without losing
// context identity, and so the eslint react-refresh/only-export-components
// rule is satisfied on ExecSessionsContext.tsx.

import { createContext } from "react";
import type { ExecClient } from "./ExecClient";
import type { ExecSessionMeta } from "./types";
import type { OpenSessionInput, OpenSessionResult } from "./ExecSessionsContext";

export interface DrawerState {
  /** Drawer expanded vs collapsed. Hidden when sessions.length === 0. */
  open: boolean;
  /** Pixels. Min 160, max 80% viewport. */
  height: number;
}

export interface ExecSessionsContextValue {
  sessions: ExecSessionMeta[];
  activeSessionId: string | null;
  drawer: DrawerState;
  openSession: (input: OpenSessionInput) => OpenSessionResult;
  focusSession: (id: string) => void;
  closeSession: (id: string) => void;
  setDrawerOpen: (open: boolean) => void;
  setDrawerHeight: (height: number) => void;
  toggleDrawer: () => void;
  /** Look up the live ExecClient for rendering a Terminal. */
  getClient: (id: string) => ExecClient | null;
  /** Banner action: skip reconnect backoff and try now. */
  reconnectNow: (id: string) => void;
  /** Banner action: abandon reconnection — flips status to error. */
  giveUpReconnect: (id: string) => void;
}

export const ExecSessionsCtx =
  createContext<ExecSessionsContextValue | null>(null);
