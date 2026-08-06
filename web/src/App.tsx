import { useEffect, useState } from "react";

import { api } from "./api/client";
import { copy } from "./copy";

type Connection = "checking" | "ok" | "down";

export function App() {
  const [connection, setConnection] = useState<Connection>("checking");

  useEffect(() => {
    let cancelled = false;

    api
      .GET("/healthz")
      .then(({ data }) => {
        if (cancelled) return;
        setConnection(data?.status === "ok" && data.database === "ok" ? "ok" : "down");
      })
      .catch(() => {
        if (!cancelled) setConnection("down");
      });

    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <main>
      <h1>Vellum</h1>
      <p>{copy.connection[connection]}</p>
    </main>
  );
}
