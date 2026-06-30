import http from '@/utils/request'
import type { AxiosRequestConfig } from 'axios'
import type { AIAnalyzeProgress, AIAnalyzeBatchResponse, AIAnalyzeTask, AIProviderInfo, AnalysisRuntimeStatus, AIBackgroundLogsResponse } from '@/types/ai'
import type { ApiResponse } from '@/types/api'

export const aiApi = {
  // 分析单张照片
  // 后端为同步分析（含两次 AI 会话，可能耗时较长），可通过 config 覆盖默认 30s 超时
  analyze(photoId: number, config?: AxiosRequestConfig) {
    return http.post<ApiResponse<void>>('/ai/analyze', { photo_id: photoId }, config)
  },

  // 批量分析
  analyzeBatch(limit: number = 100) {
    return http.post<ApiResponse<AIAnalyzeBatchResponse>>('/ai/analyze/batch', { limit })
  },

  startBackground() {
    return http.post<ApiResponse<AIAnalyzeTask>>('/ai/background/start')
  },

  stopBackground() {
    return http.post<ApiResponse<void>>('/ai/background/stop')
  },

  getBackgroundLogs() {
    return http.get<ApiResponse<AIBackgroundLogsResponse>>('/ai/background/logs')
  },

  // 获取分析进度
  // params.lite=true 时复用 /system/stats 的共享照片统计缓存，Dashboard 场景使用
  getProgress(params?: { lite?: boolean }) {
    return http.get<ApiResponse<AIAnalyzeProgress>>('/ai/progress', { params })
  },

  // 重新分析
  reAnalyze(id: number, config?: AxiosRequestConfig) {
    return http.post<ApiResponse<void>>(`/ai/reanalyze/${id}`, undefined, config)
  },

  // 获取 Provider 信息
  getProviderInfo() {
    return http.get<ApiResponse<AIProviderInfo>>('/ai/provider')
  },

  // 获取任务状态
  getTaskStatus() {
    return http.get<ApiResponse<AIAnalyzeTask>>('/ai/task')
  },

  // 获取全局分析运行状态
  getRuntimeStatus() {
    return http.get<ApiResponse<AnalysisRuntimeStatus>>('/ai/runtime')
  },
}
