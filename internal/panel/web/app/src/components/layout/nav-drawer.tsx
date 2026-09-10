import { Sheet } from "@/components/ui/sheet";
import { NavActions, NavList, Wordmark } from "./sidebar";
import { t } from "@/lib/i18n";

// Та же навигация на узком экране. Список берётся из того же nav-items.ts, что
// и колонка, — иначе на телефоне однажды не хватит раздела, и заметит это не
// тот, кто правил, а тот, кто открыл панель в дороге.

export function NavDrawer({
  open,
  onClose,
  onInvite,
  onUpload,
}: {
  open: boolean;
  onClose: () => void;
  onInvite: () => void;
  onUpload: () => void;
}) {
  return (
    <Sheet
      open={open}
      onClose={onClose}
      label={t("Разделы")}
      className="bg-[var(--sidebar-bg)] border-r-0 text-[var(--sidebar-text)]"
    >
      <div className="flex min-h-full flex-col">
        <Wordmark onClick={onClose} />
        <div className="flex-1">
          <NavList onNavigate={onClose} />
        </div>
        <NavActions onInvite={onInvite} onUpload={onUpload} onNavigate={onClose} />
      </div>
    </Sheet>
  );
}
