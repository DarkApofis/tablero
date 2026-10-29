import type { Metadata } from "next";
import { SettingsView } from "./settings-view";

export const metadata: Metadata = { title: "Ajustes · Tablero" };

// The workspace comes from the layout (WorkspaceShell).
export default function SettingsPage() {
  return <SettingsView />;
}
