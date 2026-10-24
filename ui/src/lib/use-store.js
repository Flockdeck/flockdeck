// use-store.js turns a store into a hook: useStore(store) returns the current
// snapshot and draws again when it changes.
import { useEffect, useState } from "../vendor/hooks.mjs";

export function useStore(store) {
  const [snap, setSnap] = useState(store.getState());
  useEffect(() => {
    setSnap(store.getState());
    return store.subscribe(setSnap);
  }, [store]);
  return snap;
}
