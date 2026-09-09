export function AvatarInitials({ name, size = "sm" }: { name: string; size?: "sm" | "md" }) {
  const initials = name
    ?.split(" ")
    .map((w) => w[0])
    .join("")
    .slice(0, 2)
    .toUpperCase() || "?";

  const sizeClass = size === "md"
    ? "h-10 w-10 text-sm"
    : "h-8 w-8 text-xs";

  return (
    <div className={`flex shrink-0 items-center justify-center rounded-full bg-[var(--muted)] font-semibold text-[var(--muted-foreground)] ${sizeClass}`}>
      {initials}
    </div>
  );
}

export function getInitials(name?: string | null): string {
  return name?.split(" ").map(w => w[0]).join("").slice(0, 2).toUpperCase() || "?";
}
