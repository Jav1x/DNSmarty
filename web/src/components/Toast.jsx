import { createContext, useCallback, useContext, useState } from "react";
import { ConfirmDialog } from "./ConfirmDialog";

const ToastCtx = createContext(() => {});

export function ToastProvider({ children }) {
  const [items, setItems] = useState([]);
  function push(text, kind) {
    const id = Math.random().toString(36).slice(2);
    setItems((cur) => [...cur, { id, text, kind }]);
    setTimeout(() => setItems((cur) => cur.filter((x) => x.id !== id)), 4000);
  }
  return (
    <ToastCtx.Provider value={push}>
      {children}
      <div className="toasts" role="status" aria-live="polite">
        {items.map((x) => <div key={x.id} className={`toast${x.kind === "err" ? " err" : ""}`}>{x.text}</div>)}
      </div>
    </ToastCtx.Provider>
  );
}

export function useToast() {
  return useContext(ToastCtx);
}

// useConfirm returns [ask, dialog]: ask(text, onYes) opens the dialog, onYes runs after confirmation.
export function useConfirm() {
  const [state, setState] = useState(null);
  const ask = useCallback((text, onYes, danger = true) => {
    setState({ text, onYes, danger });
  }, []);
  const dialog = state ? (
    <ConfirmDialog
      text={state.text}
      danger={state.danger}
      onYes={() => { const fn = state.onYes; setState(null); fn(); }}
      onClose={() => setState(null)}
    />
  ) : null;
  return [ask, dialog];
}
