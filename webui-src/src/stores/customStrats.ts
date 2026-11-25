import { ref } from 'vue'
import { fetchCustomStrats } from '../api/endpoints'
import type { CustomBlockInfo, CustomStrategy } from '../api/types'

export const customBlocks = ref<CustomBlockInfo[]>([])
export const customStrategies = ref<CustomStrategy[]>([])
export const customStratsLoaded = ref(false)

export async function refreshCustomStrats(block = '', signal?: AbortSignal) {
  const data = await fetchCustomStrats(block, signal)
  customBlocks.value = Array.isArray(data.blocks) ? data.blocks : []
  customStrategies.value = Array.isArray(data.strategies) ? data.strategies : []
  customStratsLoaded.value = true
  return data
}
