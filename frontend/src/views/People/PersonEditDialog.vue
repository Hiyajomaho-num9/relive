<template>
  <el-dialog
    :model-value="modelValue"
    title="编辑人物信息"
    width="420px"
    :close-on-click-modal="false"
    append-to-body
    @update:model-value="emit('update:modelValue', $event)"
    @opened="focusNameInput"
  >
    <el-form v-if="person" label-position="right" label-width="72px" class="person-edit-form">
      <!-- 人物分类：紧凑同行，选择框不占满整行 -->
      <el-form-item label="人物分类" class="person-edit-item">
        <el-select v-model="editableCategory" placeholder="选择类别" class="person-edit-category">
          <el-option
            v-for="option in categoryOptions"
            :key="option.value"
            :label="option.label"
            :value="option.value"
          />
        </el-select>
      </el-form-item>

      <!-- 人物姓名：紧跟标签，占满剩余宽度 -->
      <el-form-item label="人物姓名" class="person-edit-item">
        <el-input
          ref="nameInputRef"
          v-model="editableName"
          placeholder="输入人物姓名，留空则为未命名"
          clearable
          :maxlength="NAME_MAX_LENGTH"
          show-word-limit
        />
      </el-form-item>

      <!-- 可能是同一个人：根据姓名实时搜索 -->
      <div class="person-search">
        <div class="person-search-title">可能是同一个人</div>

        <!-- 空姓名提示 -->
        <div v-if="!searchQuery" class="person-search-hint">输入姓名后可查找已有的人物</div>

        <template v-else>
          <!-- 搜索中 -->
          <div v-if="searchLoading && results.length === 0" class="person-search-status">
            搜索中…
          </div>

          <!-- 搜索失败 -->
          <div v-else-if="searchError" class="person-search-status person-search-error">
            <span>搜索失败，</span>
            <el-button text type="primary" size="small" class="retry-link" @click="runSearch(true)">重试</el-button>
          </div>

          <!-- 无匹配结果 -->
          <div v-else-if="!searchLoading && results.length === 0" class="person-search-status">
            没有找到其他同名人物
          </div>

          <!-- 结果列表：固定高度滚动 + 继续加载 -->
          <template v-else>
            <div class="person-search-list">
              <button
                v-for="item in results"
                :key="item.id"
                type="button"
                class="person-search-item"
                :title="`将当前人物合并到「${itemDisplayName(item)}」`"
                @click="handlePickItem(item)"
              >
                <div class="person-search-avatar">
                  <img
                    v-if="item.representative_face_id && !avatarFailed.has(item.representative_face_id)"
                    :src="getFaceThumbnail(item.representative_face_id)"
                    loading="lazy"
                    alt=""
                    @error="markAvatarFailed(item.representative_face_id!)"
                  />
                  <span v-else class="person-search-avatar-fallback">{{ getPersonAvatarFallback(item) }}</span>
                </div>
                <div class="person-search-info">
                  <span class="person-search-name">{{ itemDisplayName(item) }}</span>
                  <span class="person-search-meta">
                    <span class="person-search-category" :class="`is-${item.category}`">{{ getPersonCategoryLabel(item.category) }}</span>
                    <span class="person-search-count">{{ item.photo_count }} 张照片</span>
                  </span>
                </div>
              </button>

              <!-- 继续加载 -->
              <div v-if="hasMore" class="person-search-more">
                <el-button
                  text
                  type="primary"
                  size="small"
                  :loading="loadingMore"
                  @click="loadMore"
                >
                  加载更多
                </el-button>
              </div>
              <div v-else class="person-search-end">已显示全部匹配人物</div>
            </div>
          </template>
        </template>
      </div>
    </el-form>

    <template #footer>
      <el-button @click="handleCancel">取消</el-button>
      <el-button type="primary" :loading="loading" :disabled="!hasChanges" @click="handleSave">
        保存
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { peopleApi } from '@/api/people'
import type { Person, PersonCategory } from '@/types/people'
import { getPersonAvatarFallback, getPersonCategoryLabel } from './peopleHelpers'

/** 姓名最大长度：后端 name 列为 varchar(100)，前端取合理上限 50 并显示字数 */
const NAME_MAX_LENGTH = 50

/** 搜索每页数量：避免一次渲染大量人物 */
const SEARCH_PAGE_SIZE = 20

const categoryOptions: Array<{ label: string; value: PersonCategory }> = [
  { label: '家人', value: 'family' },
  { label: '亲友', value: 'friend' },
  { label: '熟人', value: 'acquaintance' },
  { label: '路人', value: 'stranger' },
]

const props = defineProps<{
  modelValue: boolean
  person: Person | null
  loading: boolean
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  /** 仅包含发生变化的字段：name 已去除首尾空格，category 为新类别 */
  submit: [payload: { name?: string; category?: PersonCategory }]
  /** 点击搜索结果，发起合并确认。目标人物为搜索结果中所选人物 */
  merge: [target: Person]
}>()

const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'

const getFaceThumbnail = (faceId?: number) => {
  if (!faceId) return ''
  return `${apiBaseUrl}/faces/${faceId}/thumbnail?v=${faceId}`
}

const editableName = ref('')
const editableCategory = ref<PersonCategory>('stranger')
const nameInputRef = ref<{ focus?: () => void } | null>(null)

const originalName = computed(() => props.person?.name?.trim() ?? '')
const originalCategory = computed<PersonCategory>(() => props.person?.category ?? 'stranger')

const hasNameChanged = computed(() => editableName.value.trim() !== originalName.value)
const hasCategoryChanged = computed(() => editableCategory.value !== originalCategory.value)
const hasChanges = computed(() => hasNameChanged.value || hasCategoryChanged.value)

// ---- 搜索状态 ----
const searchQuery = ref('')
const results = ref<Person[]>([])
const searchLoading = ref(false)
const searchError = ref(false)
const loadingMore = ref(false)
const resultPage = ref(1)
const resultTotal = ref(0)
// 是否已加载完所有匹配页（本页返回少于页大小即视为结束）
const searchFinished = ref(false)
// 请求代际：快速连续输入时丢弃过期请求结果，避免旧结果覆盖新结果
let searchSeq = 0
let debounceTimer: number | null = null
// 头像加载失败的 faceId 集合
const avatarFailed = ref(new Set<number>())
const markAvatarFailed = (faceId: number) => {
  avatarFailed.value.add(faceId)
}

const hasMore = computed(() => !searchFinished.value && results.value.length < resultTotal.value)

const itemDisplayName = (item: Person) => (item.name?.trim() ? item.name!.trim() : '这是谁？')

// 弹框打开或编辑目标变化时，用当前人物信息重置编辑字段与搜索状态
watch(
  () => [props.modelValue, props.person?.id] as const,
  ([visible]) => {
    if (visible && props.person) {
      editableName.value = props.person.name?.trim() ?? ''
      editableCategory.value = props.person.category
      // 重置搜索：editableName 的 watch 会触发首次搜索
      results.value = []
      resultTotal.value = 0
      resultPage.value = 1
      searchFinished.value = false
      searchError.value = false
      searchLoading.value = false
      loadingMore.value = false
      avatarFailed.value = new Set()
    }
  },
)

// 姓名变化：去抖 300ms 后发起搜索
watch(editableName, value => {
  if (debounceTimer) {
    window.clearTimeout(debounceTimer)
  }
  const trimmed = value.trim()
  searchQuery.value = trimmed
  if (!trimmed) {
    // 姓名为空：清空结果，不发请求
    results.value = []
    resultTotal.value = 0
    resultPage.value = 1
    searchFinished.value = false
    searchLoading.value = false
    searchError.value = false
    return
  }
  debounceTimer = window.setTimeout(() => {
    runSearch()
  }, 300)
})

/**
 * 发起搜索。reset=true 时从第一页重新加载（用于新输入或重试）。
 * 通过 searchSeq 丢弃过期请求，避免快速输入时旧结果覆盖新结果。
 */
const runSearch = async (reset = true) => {
  const query = searchQuery.value
  if (!query) return
  // 弹框已关闭则不再发起搜索（避免关闭后挂起的去抖请求继续发请求）
  if (!props.modelValue) return
  if (reset) {
    resultPage.value = 1
  }
  const page = resultPage.value
  const mySeq = ++searchSeq
  if (reset) {
    searchLoading.value = true
  } else {
    loadingMore.value = true
  }
  searchError.value = false
  try {
    const res = await peopleApi.getList({ search: query, page, page_size: SEARCH_PAGE_SIZE })
    // 请求返回后若代际已变（输入已继续变化），丢弃结果
    if (mySeq !== searchSeq) return
    const payload = res.data?.data
    const rawItems = payload?.items || []
    const items = rawItems.filter(person => person.id !== props.person?.id)
    const totalCount = payload?.total || 0
    if (reset) {
      results.value = items
    } else {
      // 追加去重
      const existing = new Set(results.value.map(person => person.id))
      const fresh = items.filter(person => !existing.has(person.id))
      results.value = [...results.value, ...fresh]
    }
    resultTotal.value = totalCount
    resultPage.value = page + 1
    // 本页返回少于页大小，说明已无更多匹配页
    if (rawItems.length < SEARCH_PAGE_SIZE) {
      searchFinished.value = true
    }
  } catch {
    if (mySeq !== searchSeq) return
    searchError.value = true
  } finally {
    if (mySeq === searchSeq) {
      searchLoading.value = false
      loadingMore.value = false
    }
  }
}

const loadMore = () => {
  if (loadingMore.value || !hasMore.value) return
  runSearch(false)
}

const handlePickItem = (target: Person) => {
  // 不自动保存编辑内容，仅发起合并确认
  emit('merge', target)
}

const focusNameInput = () => {
  nameInputRef.value?.focus?.()
}

const handleCancel = () => {
  emit('update:modelValue', false)
}

const handleSave = () => {
  if (!props.person || !hasChanges.value) return
  const payload: { name?: string; category?: PersonCategory } = {}
  if (hasNameChanged.value) {
    // 去除首尾空格；仅空格的输入会被规整为空字符串
    payload.name = editableName.value.trim()
  }
  if (hasCategoryChanged.value) {
    payload.category = editableCategory.value
  }
  emit('submit', payload)
}
</script>

<style scoped>
.person-edit-form {
  padding-top: 4px;
}

/* 紧凑布局：减少表单项垂直留白 */
.person-edit-form :deep(.person-edit-item) {
  margin-bottom: 12px;
}

.person-edit-category {
  width: 160px;
}

/* 搜索区域 */
.person-search {
  margin-top: 4px;
  padding-left: 72px; /* 与表单控件左对齐 */
}

.person-search-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--color-text-secondary);
  margin-bottom: 8px;
}

.person-search-hint,
.person-search-status {
  font-size: 13px;
  color: var(--color-text-secondary);
  padding: 12px 0;
}

.person-search-error {
  display: flex;
  align-items: center;
  gap: 4px;
}

.person-search-list {
  max-height: 264px;
  overflow-y: auto;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-bg-soft);
}

.person-search-item {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 10px;
  border: none;
  border-bottom: 1px solid var(--color-border);
  background: transparent;
  cursor: pointer;
  text-align: left;
  transition: background 0.15s ease;
}

.person-search-item:last-child {
  border-bottom: none;
}

.person-search-item:hover {
  background: rgba(212, 107, 8, 0.06);
}

.person-search-item:focus-visible {
  outline: 2px solid var(--color-primary, #d46b08);
  outline-offset: -2px;
}

.person-search-item:active {
  background: rgba(212, 107, 8, 0.12);
}

.person-search-avatar {
  width: 40px;
  height: 40px;
  border-radius: 8px;
  overflow: hidden;
  background: #fff;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}

.person-search-avatar img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

.person-search-avatar-fallback {
  font-size: 18px;
  font-weight: 600;
  color: var(--color-text-secondary);
}

.person-search-info {
  min-width: 0;
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.person-search-name {
  font-size: 14px;
  font-weight: 600;
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.person-search-meta {
  display: flex;
  align-items: center;
  gap: 8px;
}

.person-search-category {
  font-size: 11px;
  font-weight: 600;
  padding: 1px 7px;
  border-radius: 999px;
  white-space: nowrap;
}

.person-search-category.is-family {
  background: rgba(245, 108, 108, 0.12);
  color: #c45656;
}
.person-search-category.is-friend {
  background: rgba(103, 194, 58, 0.14);
  color: #5a9a3a;
}
.person-search-category.is-acquaintance {
  background: rgba(230, 162, 60, 0.14);
  color: #b8821f;
}
.person-search-category.is-stranger {
  background: rgba(144, 147, 153, 0.14);
  color: #8a8d93;
}

.person-search-count {
  font-size: 12px;
  color: var(--color-text-secondary);
}

.person-search-more {
  display: flex;
  justify-content: center;
  padding: 6px 0;
}

.person-search-end {
  text-align: center;
  font-size: 12px;
  color: var(--color-text-secondary);
  padding: 8px 0;
}

/* 窄屏：标签与控件自然换行，搜索区左对齐 */
@media (max-width: 480px) {
  .person-search {
    padding-left: 0;
  }
}
</style>
