import bgUrl from "./assets/home_bg.webp";

export function App() {
  return (
    <img
      src={bgUrl}
      alt=""
      aria-hidden="true"
      fetchPriority="high"
      /* 竖屏时把焦点下移到 70%，否则 cover 会从横图中间切一条窄缝，人物全被裁掉 */
      className="fixed inset-0 -z-10 h-full w-full object-cover object-center portrait:object-[50%_70%]"
    />
  );
}
