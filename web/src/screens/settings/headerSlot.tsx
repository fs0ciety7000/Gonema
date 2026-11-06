/* The settings page header (title + intro) has a slot on its right for the open section's own
   actions: "Back up now" on Backups, the server log level on Logs (settings-backups.html,
   settings-logs.html). Sections portal their controls into it. */
import { createContext, type ReactNode, useContext } from 'react';
import { createPortal } from 'react-dom';

export const HeaderSlot = createContext<HTMLElement | null>(null);

export function HeaderActions({ children }: { children: ReactNode }) {
  const el = useContext(HeaderSlot);
  return el ? createPortal(children, el) : null;
}
