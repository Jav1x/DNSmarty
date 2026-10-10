import { useEffect, useRef, useState } from "react";
import { ApiError, api } from "../api";

// useLoad fetches a path and re-fetches when it changes. 401 redirects to /login.
export function useLoad(path) {
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  async function reload() {
    try {
      setData(await api(path));
      setError("");
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        window.location.assign("/login");
        return;
      }
      setError(e.message);
    } finally {
      setLoading(false);
    }
  }
  useEffect(() => { setLoading(true); reload(); }, [path]);
  return { data, loading, error, setError, reload };
}

// usePoll re-runs fn on an interval, pausing while the tab is hidden. fn may change
// between renders; the timer is not reset by that.
export function usePoll(fn, ms) {
  const ref = useRef(fn);
  ref.current = fn;
  useEffect(() => {
    let timer = null;
    function tick() {
      timer = setTimeout(() => {
        if (!document.hidden) ref.current();
        tick();
      }, ms);
    }
    tick();
    return () => clearTimeout(timer);
  }, [ms]);
}

