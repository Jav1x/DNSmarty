import Tip from "./Tip"

export default function Lamp({ state = "dis", tip }) {
  const el = <span className={`st ${state}`}><span className="lamp" /></span>
  return tip ? <Tip label={tip}>{el}</Tip> : el
}
