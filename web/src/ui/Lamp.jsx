import Tip from "./Tip"

/** Лампа статуса `.st .lamp` (ui.css): on — синяя пульс, warn — янтарная,
    off — красная, dis — серая. tip — тултип с состоянием. */
export default function Lamp({ state = "dis", tip }) {
  const el = <span className={`st ${state}`}><span className="lamp" /></span>
  return tip ? <Tip label={tip}>{el}</Tip> : el
}
