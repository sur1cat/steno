import type { ClipboardEvent } from "react";

const pad2 = (n: number) => String(n).padStart(2, "0");

// Build an ISO YYYY-MM-DD string, returning null for impossible calendar dates
// (e.g. 31.02). new Date(...) does the real-date check; we also reject the
// rollover it would otherwise do silently (Feb 31 → Mar 03).
function buildIso(year: number, month: number, day: number): string | null {
  if (month < 1 || month > 12 || day < 1 || day > 31) return null;
  const iso = `${year}-${pad2(month)}-${pad2(day)}`;
  const dt = new Date(`${iso}T00:00:00Z`);
  if (Number.isNaN(dt.getTime())) return null;
  if (
    dt.getUTCFullYear() !== year ||
    dt.getUTCMonth() + 1 !== month ||
    dt.getUTCDate() !== day
  ) {
    return null;
  }
  return iso;
}

// Parse the date formats operators paste from 1C/Excel into the ISO string a
// native <input type="date"> accepts. Returns null for anything unrecognised
// so the caller can fall back to the browser's default paste handling.
export function parsePastedDateToIso(input: string): string | null {
  const s = input.trim();
  if (!s) return null;

  // Already ISO (optionally carrying a time component we discard).
  const iso = s.match(/^(\d{4})-(\d{1,2})-(\d{1,2})/);
  if (iso) return buildIso(+iso[1], +iso[2], +iso[3]);

  // KZ-locale day-first: DD.MM.YYYY / DD/MM/YYYY / DD-MM-YYYY (single-digit
  // day/month allowed). The 4-digit year is required so this can't collide
  // with the ISO branch above.
  const dmy = s.match(/^(\d{1,2})[./-](\d{1,2})[./-](\d{4})$/);
  if (dmy) return buildIso(+dmy[3], +dmy[2], +dmy[1]);

  return null;
}

// onPaste handler for native date inputs. Native <input type="date"> rejects
// pasted text like "01.01.1990" outright — operators can only type or pick.
// We parse common formats to ISO, set the value through the native setter and
// dispatch an input event so React Hook Form's registered onChange picks it up
// (the standard controlled-input bridge). Unrecognised text is left to the
// browser's default handling.
export function handleDateInputPaste(e: ClipboardEvent<HTMLInputElement>): void {
  const iso = parsePastedDateToIso(e.clipboardData.getData("text"));
  if (!iso) return;
  e.preventDefault();
  const input = e.currentTarget;
  const setValue = Object.getOwnPropertyDescriptor(
    window.HTMLInputElement.prototype,
    "value",
  )?.set;
  if (!setValue) return;
  setValue.call(input, iso);
  input.dispatchEvent(new Event("input", { bubbles: true }));
}
