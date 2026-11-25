import { ref } from 'vue'
import { fetchCustomBlobs } from '../api/endpoints'
import type { CustomBlobsPayload, SniProfileInfo, SniSetInfo } from '../api/types'

// Единый SNI- и blob-стейт для вкладки «Блобы и SNI»:
// /api/blobs отдаёт и список пользовательских блобов, и SNI-блок за один запрос.
export const sniProfiles = ref<SniProfileInfo[]>([])
export const sniSets = ref<SniSetInfo[]>([])
export const sniUnavailable = ref(false)
export const customBlobsPayload = ref<CustomBlobsPayload | null>(null)

export function applySniPayload(data: CustomBlobsPayload) {
  customBlobsPayload.value = data
  sniUnavailable.value = !data.sni
  sniProfiles.value = Array.isArray(data.sni?.profiles) ? data.sni.profiles : []
  sniSets.value = Array.isArray(data.sni?.sets) ? data.sni.sets : []
}

export async function refreshSniState(signal?: AbortSignal): Promise<CustomBlobsPayload> {
  const data = await fetchCustomBlobs(signal)
  applySniPayload(data)
  return data
}
