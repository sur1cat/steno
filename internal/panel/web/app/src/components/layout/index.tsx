import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { useLocation } from "react-router-dom";
import { cn } from "@/lib/utils";
import { InviteDialog } from "@/components/invite-dialog";
import { UploadDialog } from "@/components/upload-dialog";
import { Header } from "./header";
import { NavDrawer } from "./nav-drawer";
import { Sidebar } from "./sidebar";

export { PageHead, GroupHead, Empty, Loading, Failed } from "./page";

/**
 * Страницы, где содержимое — сетка карточек, а не колонка текста.
 *
 * Узкая колонка заведена ради строки текста: читать строку в 1200 пикселей
 * тяжело, глаз теряет начало следующей. К карточкам это рассуждение не
 * применимо вовсе — у них нет длины строки, зато есть соседи, и на широком
 * экране узкая колонка оставляет их стоять полосой посреди пустоты.
 *
 * Список, а не свойство страницы: ширина — это про рамку вокруг содержимого,
 * и решать её там же, где решается всё остальное про рамку, честнее, чем
 * заставлять каждую страницу объявлять её через контекст.
 */
const GRID_ROUTES = ["/projects"];

function isGrid(pathname: string): boolean {
  return GRID_ROUTES.includes(pathname);
}

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
  const { pathname } = useLocation();
  const scroller = useRef<HTMLElement>(null);

  // Новая страница показывается сверху.
  //
  // Прокручивается не окно, а <main>, и его позицию не сбрасывает никто:
  // браузер к своему восстановлению прокрутки этот контейнер не относит, а
  // роутер про него не знает. Промотал расшифровку, ушёл в раздел — и попал в
  // его середину: заголовок уехал под шапку, под содержимым пусто, и выглядит
  // это не как «я прокручен», а как сломанная вёрстка. Само оно чинится только
  // когда новая страница короче экрана целиком: тогда браузер прижимает
  // позицию к нулю, и до этой правки казалось, что сброс не нужен.
  //
  // useLayoutEffect, а не useEffect: сброс должен случиться до отрисовки, иначе
  // человек увидит кадр с чужой позицией. И раньше, чем страница созвона
  // доедет по ?t= до нужной строки, — та перемотка живёт в обычном эффекте и
  // отрабатывает после этого.
  useLayoutEffect(() => {
    scroller.current?.scrollTo({ top: 0 });
  }, [pathname]);

  const invite = () => setInviting(true);
  const upload = () => setUploading(true);

  return (
    <div className="flex h-screen overflow-hidden bg-[var(--background)] text-[var(--foreground)]">
      <Sidebar onInvite={invite} onUpload={upload} />

      <div className="flex min-w-0 flex-1 flex-col">
        <Header onOpenNav={() => setNavOpen(true)} />
        {/* Колонка текста узкая: читать строку в 1200 пикселей тяжело, глаз
            теряет начало следующей. Сетке карточек это ограничение ни к чему —
            см. GRID_ROUTES выше. */}
        <main ref={scroller} className="flex-1 overflow-y-auto">
          <div
            className={cn(
              "mx-auto w-full px-4 py-6 sm:px-6 sm:py-8",
              isGrid(pathname) ? "max-w-7xl" : "max-w-4xl",
            )}
          >
            {children}
          </div>
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
