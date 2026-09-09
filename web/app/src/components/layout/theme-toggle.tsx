import { useRef } from "react";
import { Check, Monitor, Moon, Sun } from "lucide-react";
import { cn } from "@/lib/utils";
import { useTheme, type ThemeChoice } from "@/lib/theme";
import { DropdownItem, DropdownMenu } from "@/components/ui/dropdown-menu";

const OPTIONS: { value: ThemeChoice; label: string; icon: typeof Sun }[] = [
  { value: "light", label: "Светлая", icon: Sun },
  { value: "dark", label: "Тёмная", icon: Moon },
  { value: "system", label: "Как в системе", icon: Monitor },
];

// Выбор темы. Не переключатель на два положения: «как в системе» — это
// отдельный осмысленный выбор, и после первого же нажатия на двухпозиционный
// переключатель вернуться к нему было бы нечем.
//
// Круг раскрытия расходится из этой кнопки, а не из пункта меню: меню к моменту
// перерисовки уже закрыто и снято с экрана, и его прямоугольник ничего не
// значит.
export function ThemeToggle({ className }: { className?: string }) {
  const { choice, resolved, setTheme } = useTheme();
  const trigger = useRef<HTMLButtonElement>(null);
  const Icon = resolved === "dark" ? Moon : Sun;

  return (
    <DropdownMenu
      trigger={
        <button
          ref={trigger}
          type="button"
          aria-label="Тема оформления"
          className={cn(
            "flex h-9 w-9 items-center justify-center rounded-xl text-[var(--muted-foreground)] transition-colors hover:bg-[var(--muted)] hover:text-[var(--foreground)]",
            className,
          )}
        >
          <Icon className="h-[18px] w-[18px]" />
        </button>
      }
    >
      {OPTIONS.map((o) => (
        <DropdownItem key={o.value} onClick={() => setTheme(o.value, trigger.current)}>
          <o.icon className="h-4 w-4 shrink-0" />
          <span className="flex-1 text-left">{o.label}</span>
          <Check
            className={cn("h-4 w-4 shrink-0 text-primary", choice !== o.value && "invisible")}
          />
        </DropdownItem>
      ))}
    </DropdownMenu>
  );
}
