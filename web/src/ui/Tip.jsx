import * as RT from "@radix-ui/react-tooltip"

/** Radix Tooltip в виде лабовой плашки .rtip (ui.css).
    label — строка (mono 10.5px, \n разрешён). children — элемент-триггер.
    Provider — внутри компонента (self-sufficient, вложенные провайдеры допустимы). */
export default function Tip({ label, children }) {
  return (
    <RT.Provider delayDuration={200}>
      <RT.Root>
        <RT.Trigger asChild>{children}</RT.Trigger>
        <RT.Portal>
          <RT.Content className="rtip" sideOffset={4} collisionPadding={8}>{label}</RT.Content>
        </RT.Portal>
      </RT.Root>
    </RT.Provider>
  )
}
