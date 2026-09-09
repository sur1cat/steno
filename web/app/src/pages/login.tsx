import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";

// Пароль общий на команду: заводить учётку каждому ради архива созвонов —
// работа, которую никто не сделает, а без неё панель просто не откроют.
export function LoginPage() {
  const [password, setPassword] = useState("");
  const qc = useQueryClient();

  const login = useMutation({
    mutationFn: () => api.login(password),
    onSuccess: (r) => qc.setQueryData(["session"], r),
  });

  return (
    <div className="grid h-screen place-items-center bg-[var(--background)] px-5">
      <Card className="w-full max-w-sm p-7">
        <h1 className="text-2xl tracking-tight">
          ste<span className="text-primary">no</span>
        </h1>
        <p className="mt-1 text-sm text-[var(--muted-foreground)]">Архив созвонов команды</p>

        <form
          className="mt-6 space-y-3"
          onSubmit={(e) => {
            e.preventDefault();
            login.mutate();
          }}
        >
          <input
            type="password"
            name="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="Пароль"
            autoFocus
            autoComplete="current-password"
            className="h-12 w-full rounded-xl border-0 bg-[var(--muted)] px-4 text-sm placeholder:text-[var(--muted-foreground)]/60 focus:outline-none focus:ring-2 focus:ring-primary/30"
          />
          {login.isError && (
            <p className="text-sm text-[var(--destructive)]">Не тот пароль.</p>
          )}
          <Button type="submit" variant="primary" className="w-full" isLoading={login.isPending}>
            Войти
          </Button>
        </form>
      </Card>
    </div>
  );
}
