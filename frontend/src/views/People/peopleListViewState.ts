import type { Person, PersonCategory } from '@/types/people'

export type BrowseMode = 'pagination' | 'continuous'

/**
 * 连续浏览模式的视图快照。
 *
 * 设计目标：从人物详情页返回人物列表时，恢复已加载的人物与滚动位置；
 * 主动刷新页面（整页重载）时允许从第一页重新加载。
 *
 * 因此该快照保存在模块级单例中——它在组件卸载/重新挂载之间存活
 *（导航到详情页再返回），但在整页刷新时随 JS 运行时一起丢失，
 * 正好满足「返回恢复、刷新重载」的诉求。
 */
export interface ContinuousViewSnapshot {
  items: Person[]
  nextPage: number
  total: number
  finished: boolean
  search: string
  category: PersonCategory | undefined
  pageSize: number
  scrollTop: number
}

const emptySnapshot: ContinuousViewSnapshot = {
  items: [],
  nextPage: 1,
  total: 0,
  finished: false,
  search: '',
  category: undefined,
  pageSize: 50,
  scrollTop: 0,
}

let snapshot: ContinuousViewSnapshot = { ...emptySnapshot }

export function getContinuousSnapshot(): ContinuousViewSnapshot {
  return snapshot
}

/**
 * 离开列表页（进入人物详情）前保存当前连续浏览状态。
 */
export function saveContinuousSnapshot(data: ContinuousViewSnapshot) {
  snapshot = { ...data, items: [...data.items] }
}

/**
 * 判断快照是否与当前筛选条件匹配——只有匹配时才能用于恢复，
 * 否则应丢弃并从第一页重新加载。
 */
export function isContinuousSnapshotUsable(
  search: string,
  category: PersonCategory | undefined,
  pageSize: number,
): boolean {
  return (
    snapshot.items.length > 0 &&
    snapshot.search === search &&
    snapshot.category === category &&
    snapshot.pageSize === pageSize
  )
}

export function clearContinuousSnapshot() {
  snapshot = { ...emptySnapshot }
}

const MODE_STORAGE_KEY = 'relive.people.browseMode'

export function loadBrowseMode(): BrowseMode {
  try {
    const value = window.localStorage.getItem(MODE_STORAGE_KEY)
    if (value === 'pagination' || value === 'continuous') {
      return value
    }
  } catch {
    // localStorage 不可用时忽略，使用默认值
  }
  return 'pagination'
}

export function saveBrowseMode(mode: BrowseMode) {
  try {
    window.localStorage.setItem(MODE_STORAGE_KEY, mode)
  } catch {
    // 忽略写入失败
  }
}
