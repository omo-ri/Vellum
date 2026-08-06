import { useEffect, useState } from "react";

import { api } from "./api/client";
import { copy } from "./copy";

// 骨架阶段的整个前端就是这一件事：向后端要一次健康状态，把结果显示出来。
//
// 它同时也是这一批的验收标准——页面上出现「据点连接正常」，就证明 Vite proxy、
// Go 服务、环境变量读取、pgx 连接池、goose 迁移五个环节全部接上了。
// 任何一环断掉，这里都会显示「连接不上」。
type Connection = "checking" | "ok" | "down";

export function App() {
  const [connection, setConnection] = useState<Connection>("checking");

  useEffect(() => {
    // StrictMode 在开发时会把 effect 跑两遍；用它来忽略先前那次的结果，
    // 免得两次响应互相覆盖。
    let cancelled = false;

    api
      .GET("/healthz")
      .then(({ data }) => {
        if (cancelled) return;
        // 数据库不通时后端回 503，openapi-fetch 会把 data 留空——因此这里
        // 只有「两项都 ok」才算连上。
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
