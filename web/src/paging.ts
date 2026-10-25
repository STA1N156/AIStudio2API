import { computed, ref, watch, type Ref } from 'vue'

// usePaged 把列表切成固定大小的页，只渲染当前页；列表变短时退回最后一页
export function usePaged<T>(items: Readonly<Ref<readonly T[]>>, size: number) {
  const page = ref(1)
  const pages = computed(() => Math.max(1, Math.ceil(items.value.length / size)))
  const paged = computed(() => items.value.slice((page.value - 1) * size, page.value * size))
  watch(pages, (count) => {
    if (page.value > count) page.value = count
  })
  return { page, pages, paged }
}
