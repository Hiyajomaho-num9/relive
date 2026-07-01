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
    <el-form v-if="person" label-position="top" class="person-edit-form">
      <el-form-item label="人物姓名">
        <el-input
          ref="nameInputRef"
          v-model="editableName"
          placeholder="输入人物姓名，留空则为未命名"
          clearable
          :maxlength="NAME_MAX_LENGTH"
          show-word-limit
        />
      </el-form-item>

      <el-form-item label="人物类别">
        <el-select v-model="editableCategory" placeholder="选择类别" style="width: 100%">
          <el-option
            v-for="option in categoryOptions"
            :key="option.value"
            :label="option.label"
            :value="option.value"
          />
        </el-select>
      </el-form-item>
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
import type { Person, PersonCategory } from '@/types/people'

/** 姓名最大长度：后端 name 列为 varchar(100)，前端取合理上限 50 并显示字数 */
const NAME_MAX_LENGTH = 50

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
}>()

const editableName = ref('')
const editableCategory = ref<PersonCategory>('stranger')
const nameInputRef = ref<{ focus?: () => void } | null>(null)

const originalName = computed(() => props.person?.name?.trim() ?? '')
const originalCategory = computed<PersonCategory>(() => props.person?.category ?? 'stranger')

const hasNameChanged = computed(() => editableName.value.trim() !== originalName.value)
const hasCategoryChanged = computed(() => editableCategory.value !== originalCategory.value)
const hasChanges = computed(() => hasNameChanged.value || hasCategoryChanged.value)

// 弹框打开或编辑目标变化时，用当前人物信息重置编辑字段
watch(
  () => [props.modelValue, props.person?.id] as const,
  ([visible]) => {
    if (visible && props.person) {
      editableName.value = props.person.name?.trim() ?? ''
      editableCategory.value = props.person.category
    }
  },
)

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
</style>
