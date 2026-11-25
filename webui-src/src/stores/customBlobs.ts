import { customBlobsPayload, refreshSniState } from './sni'

export { customBlobsPayload }

// Делегирует в общий GET /api/blobs (stores/sni.ts), чтобы вкладка делала ровно один запрос.
export const refreshCustomBlobs = refreshSniState
