// App — root layout for the data router.
//
// Owns app-wide chrome: the Cmd+K palette, the exec drawer, the
// toaster, the global fetching indicator, and the auth/theme/loading
// gates. Route definitions live in routes.tsx
// (createRoutesFromElements) and render through the Outlet below.

import { useEffect, useState } from "react";
import { Outlet } from "react-router-dom";
import { ExecSessionsProvider } from "./exec/ExecSessionsContext";
import { Drawer } from "./exec/Drawer";
import { Toaster } from "./lib/toast";
import { SearchPalette } from "./components/search/SearchPalette";
import { GlobalFetchingBar } from "./components/shell/GlobalFetchingBar";
import { useAuth } from "./auth/useAuth";
import { LoginScreen } from "./auth/LoginScreen";
import { useTheme } from "./hooks/useTheme";

export default function App() {
  const { user, isLoading } = useAuth();
  const [searchOpen, setSearchOpen] = useState(false);

  // Global Cmd+K / Ctrl+K opens the search palette. Capture phase +
  // stopPropagation so it wins against any in-page listeners (and
  // does not collide with the drawer's Cmd+` toggle).
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const meta = e.metaKey || e.ctrlKey;
      if (!meta) return;
      if (e.key !== "k" && e.key !== "K" && e.code !== "KeyK") return;
      if (e.shiftKey || e.altKey) return;
      e.preventDefault();
      e.stopPropagation();
      setSearchOpen((v) => !v);
    }
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, []);

  useTheme();

  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center bg-bg">
        <span
          aria-hidden
          className="block size-3.5 animate-spin rounded-full border-[1.5px] border-border-strong border-t-accent"
        />
      </div>
    );
  }
  if (!user) {
    return <LoginScreen />;
  }

  return (
    <ExecSessionsProvider>
      <div className="flex h-full flex-col">
        <GlobalFetchingBar />
        <div className="min-h-0 flex-1 overflow-hidden">
          <Outlet />
        </div>
        <SearchPalette open={searchOpen} onClose={() => setSearchOpen(false)} />
        <Drawer />
        <Toaster />
      </div>
    </ExecSessionsProvider>
  );
}
