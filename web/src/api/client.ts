import createClient from "openapi-fetch";
import type { paths } from "./schema";

// 全站唯一的 API 客户端。paths 由 api/openapi.yaml 生成，因此路径、方法、
// 请求体与响应体的类型全部由契约决定——写错一个路径就是一个编译错误。
//
// baseUrl 是相对路径：开发时由 Vite 的 proxy 转给 :8080，生产上由同一个 Go
// 二进制自己提供。两种情况下前端代码完全一样，也就没有「API 地址」这个配置项。
export const api = createClient<paths>({ baseUrl: "/api" });
