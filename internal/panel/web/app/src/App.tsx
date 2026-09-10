import { QueryCache, QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { Toaster } from "sonner";
import { ApiError, api } from "@/lib/api";
import { Layout } from "@/components/layout";
import { LoginPage } from "@/pages/login";
import { MeetingsPage } from "@/pages/meetings";
import { MeetingPage } from "@/pages/meeting";
import { SchedulePage } from "@/pages/schedule";
import { SearchPage } from "@/pages/search";
import { TasksPage } from "@/pages/tasks";
import { ProjectsPage } from "@/pages/projects";
import { ProjectPage } from "@/pages/project";
import { SettingsPage } from "@/pages/settings";

// Протухшая cookie не должна выглядеть как поломка: любой 401 — это «пора
// войти заново», и решается он одним местом на всё приложение.
const qc = new QueryClient({
  queryCache: new QueryCache({
    onError: (err) => {
      if (err instanceof ApiError && err.status === 401) {
        qc.setQueryData(["session"], { authenticated: false });
      }
    },
  }),
  defaultOptions: {
    queries: {
      retry: (count, err) => !(err instanceof ApiError && err.status < 500) && count < 2,
      refetchOnWindowFocus: false,
      staleTime: 15_000,
    },
  },
});

function Routed() {
  const session = useQuery({ queryKey: ["session"], queryFn: api.session, staleTime: 0 });

  if (session.isLoading) {
    return <div className="grid h-screen place-items-center text-sm text-[var(--muted-foreground)]">…</div>;
  }
  if (!session.data?.authenticated) return <LoginPage />;

  return (
    <Layout>
      <Routes>
        <Route path="/" element={<MeetingsPage />} />
        <Route path="/m/:id" element={<MeetingPage />} />
        <Route path="/schedule" element={<SchedulePage />} />
        <Route path="/search" element={<SearchPage />} />
        <Route path="/tasks" element={<TasksPage />} />
        <Route path="/projects" element={<ProjectsPage />} />
        <Route path="/p/:name" element={<ProjectPage />} />
        <Route path="/settings" element={<SettingsPage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </Layout>
  );
}

export function App() {
  return (
    <QueryClientProvider client={qc}>
      <BrowserRouter>
        <Routed />
        <Toaster position="bottom-right" richColors closeButton />
      </BrowserRouter>
    </QueryClientProvider>
  );
}
