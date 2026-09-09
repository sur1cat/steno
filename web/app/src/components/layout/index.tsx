import { useState, type ReactNode } from "react";
import { InviteDialog } from "@/components/invite-dialog";
import { UploadDialog } from "@/components/upload-dialog";
import { Header } from "./header";
import { NavDrawer } from "./nav-drawer";
import { Sidebar } from "./sidebar";

export { PageHead, GroupHead, Empty, Loading, Failed } from "./page";

// Оболочка приложения: колонка разделов слева, шапка сверху, прокручивается
// только содержимое. Ни колонка, ни шапка не уезжают — искать раздел, промотав
// часовую расшифровку до конца, не придётся.
//
// Оба диалога, заводящих созвон, живут здесь, а не в кнопках: позвать бота
// можно и из расписания, а загрузить запись — с любой страницы, и держать по
// экземпляру диалога на каждую кнопку незачем.
export function Layout({ children }: { children: ReactNode }) {
  const [navOpen, setNavOpen] = useState(false);
  const [inviting, setInviting] = useState(false);
  const [uploading, setUploading] = useState(false);

  const invite = () => setInviting(true);
  const upload = () => setUploading(true);

  return (
    <div className="flex h-screen overflow-hidden bg-[var(--background)] text-[var(--foreground)]">
      <Sidebar onInvite={invite} onUpload={upload} />

      <div className="flex min-w-0 flex-1 flex-col">
        <Header onOpenNav={() => setNavOpen(true)} />
        {/* max-w-4xl, а не во всю ширину: на 1440 строка списка растягивалась
            на 1200 пикселей ради текста, который кончался на трети, и остаток
            оставался пустым полем. Ширина колонки текста здесь та же, что у
            расшифровки, — читать её всё равно придётся глазами. */}
        <main className="flex-1 overflow-y-auto">
          <div className="mx-auto w-full max-w-4xl px-4 py-6 sm:px-6 sm:py-8">{children}</div>
        </main>
      </div>

      <NavDrawer
        open={navOpen}
        onClose={() => setNavOpen(false)}
        onInvite={invite}
        onUpload={upload}
      />
      <InviteDialog open={inviting} onClose={() => setInviting(false)} />
      <UploadDialog open={uploading} onClose={() => setUploading(false)} />
    </div>
  );
}
