import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";

export function App() {
  return (
    <div className="p-8 space-y-4">
      <h1 className="text-xl font-semibold">steno</h1>
      <Card className="p-4">Панель собирается.</Card>
      <Button>Кнопка</Button>
    </div>
  );
}
