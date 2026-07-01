<template>
  <el-dialog
    :model-value="modelValue"
    title="合并人物"
    width="520px"
    :close-on-click-modal="false"
    :close-on-press-escape="!loading"
    :show-close="!loading"
    append-to-body
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div class="merge-confirm-body">
      <!-- 来源 → 目标：桌面端横向，窄屏纵向 -->
      <div class="merge-persons" :class="{ 'is-merging': loading }">
        <div class="merge-person-card is-source">
          <div class="merge-person-tag">当前人物将被合并</div>
          <div class="merge-person-avatar">
            <img
              v-if="sourceAvatarSrc && !sourceAvatarFailed"
              :src="sourceAvatarSrc"
              alt=""
              @error="sourceAvatarFailed = true"
            />
            <span v-else class="merge-person-fallback">{{ sourceFallback }}</span>
          </div>
          <div class="merge-person-name">{{ sourceDisplayName }}</div>
          <div class="merge-person-category" :class="`is-${source?.category}`">{{ sourceCategoryLabel }}</div>
        </div>

        <div class="merge-direction">
          <el-icon><Right /></el-icon>
          <span class="merge-direction-text">合并到</span>
        </div>

        <div class="merge-person-card is-target">
          <div class="merge-person-tag is-target-tag">目标人物将被保留</div>
          <div class="merge-person-avatar">
            <img
              v-if="targetAvatarSrc && !targetAvatarFailed"
              :src="targetAvatarSrc"
              alt=""
              @error="targetAvatarFailed = true"
            />
            <span v-else class="merge-person-fallback">{{ targetFallback }}</span>
          </div>
          <div class="merge-person-name">{{ targetDisplayName }}</div>
          <div class="merge-person-category" :class="`is-${target?.category}`">{{ targetCategoryLabel }}</div>
        </div>
      </div>

      <el-alert
        type="warning"
        :closable="false"
        show-icon
        class="merge-warning"
      >
        即将把当前人物合并到所选人物。合并后，当前人物的所有人脸和照片关联将转移到目标人物，此操作无法撤销。
      </el-alert>

      <el-alert
        v-if="error"
        type="error"
        :closable="false"
        show-icon
        class="merge-error"
      >
        {{ error }}
      </el-alert>
    </div>

    <template #footer>
      <el-button :disabled="loading" @click="handleCancel">取消</el-button>
      <el-button type="danger" :loading="loading" @click="handleConfirm">
        {{ loading ? '正在合并' : '确认合并' }}
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Right } from '@element-plus/icons-vue'
import type { Person } from '@/types/people'
import { getPersonAvatarFallback, getPersonCategoryLabel } from './peopleHelpers'

const props = defineProps<{
  modelValue: boolean
  /** 来源人物（当前编辑的人物），合并后被移除 */
  source: Person | null
  /** 目标人物（搜索结果中所选），合并后保留 */
  target: Person | null
  /** 提交合并中 */
  loading: boolean
  /** 合并失败/超时错误信息，非空时展示并保留弹框 */
  error?: string
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  confirm: []
}>()

const apiBaseUrl = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080/api/v1'

const getFaceThumbnail = (faceId?: number) => {
  if (!faceId) return ''
  return `${apiBaseUrl}/faces/${faceId}/thumbnail?v=${faceId}`
}

// 头像加载失败状态：每次弹框打开/目标变化时重置
const sourceAvatarFailed = ref(false)
const targetAvatarFailed = ref(false)

watch(
  () => [props.modelValue, props.source?.id, props.target?.id] as const,
  ([visible]) => {
    if (visible) {
      sourceAvatarFailed.value = false
      targetAvatarFailed.value = false
    }
  },
)

const sourceAvatarSrc = computed(() =>
  props.source?.representative_face_id ? getFaceThumbnail(props.source.representative_face_id) : '',
)
const targetAvatarSrc = computed(() =>
  props.target?.representative_face_id ? getFaceThumbnail(props.target.representative_face_id) : '',
)

const sourceFallback = computed(() => (props.source ? getPersonAvatarFallback(props.source) : '人'))
const targetFallback = computed(() => (props.target ? getPersonAvatarFallback(props.target) : '人'))

const hasName = (p: Person | null) => !!p?.name?.trim()
const sourceDisplayName = computed(() => (hasName(props.source) ? props.source!.name!.trim() : '这是谁？'))
const targetDisplayName = computed(() => (hasName(props.target) ? props.target!.name!.trim() : '这是谁？'))
const sourceCategoryLabel = computed(() => getPersonCategoryLabel(props.source?.category))
const targetCategoryLabel = computed(() => getPersonCategoryLabel(props.target?.category))

const handleCancel = () => {
  emit('update:modelValue', false)
}

const handleConfirm = () => {
  emit('confirm')
}
</script>

<style scoped>
.merge-confirm-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.merge-persons {
  display: flex;
  align-items: stretch;
  justify-content: center;
  gap: 16px;
}

.merge-person-card {
  flex: 1 1 0;
  min-width: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  padding: 16px 12px;
  border: 1px solid var(--color-border);
  border-radius: 12px;
  background: var(--color-bg-soft);
}

.merge-person-card.is-source {
  background: rgba(245, 108, 108, 0.06);
  border-color: rgba(245, 108, 108, 0.3);
}

.merge-person-card.is-target {
  background: rgba(103, 194, 58, 0.06);
  border-color: rgba(103, 194, 58, 0.3);
}

.merge-person-tag {
  font-size: 12px;
  font-weight: 600;
  color: #c45656;
  padding: 2px 8px;
  border-radius: 999px;
  background: rgba(245, 108, 108, 0.12);
}

.merge-person-tag.is-target-tag {
  color: #5a9a3a;
  background: rgba(103, 194, 58, 0.14);
}

.merge-person-avatar {
  width: 64px;
  height: 64px;
  border-radius: 12px;
  overflow: hidden;
  background: #fff;
  border: 1px solid var(--color-border);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.merge-person-avatar img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

.merge-person-fallback {
  font-size: 26px;
  font-weight: 600;
  color: var(--color-text-secondary);
}

.merge-person-name {
  font-size: 15px;
  font-weight: 600;
  color: var(--color-text-primary);
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.merge-person-category {
  font-size: 12px;
  font-weight: 600;
  padding: 2px 8px;
  border-radius: 999px;
  white-space: nowrap;
}

.merge-person-category.is-family {
  background: rgba(245, 108, 108, 0.12);
  color: #c45656;
}
.merge-person-category.is-friend {
  background: rgba(103, 194, 58, 0.14);
  color: #5a9a3a;
}
.merge-person-category.is-acquaintance {
  background: rgba(230, 162, 60, 0.14);
  color: #b8821f;
}
.merge-person-category.is-stranger {
  background: rgba(144, 147, 153, 0.14);
  color: #8a8d93;
}

.merge-direction {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 4px;
  color: var(--color-primary, #d46b08);
  font-size: 22px;
  flex-shrink: 0;
}

.merge-direction-text {
  font-size: 11px;
  color: var(--color-text-secondary);
}

.merge-warning {
  margin: 0;
}

.merge-error {
  margin: 0;
}

/* 窄屏：两个人物纵向堆叠，方向箭头朝下 */
@media (max-width: 480px) {
  .merge-persons {
    flex-direction: column;
  }
  .merge-direction {
    transform: rotate(90deg);
  }
}
</style>
