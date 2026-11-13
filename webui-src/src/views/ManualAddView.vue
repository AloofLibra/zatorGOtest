<script setup lang="ts">
import { computed, ref } from 'vue'
import { applySetting, registerStrategy, uploadBlob } from '../api/endpoints'
import { busyActive, busyButton, withBusy } from '../stores/busy'
import { refreshTlsBlobSettings, refreshWgBlobSettings, tlsBlobSettings, wgBlobSettings } from '../stores/settings'
import { fetchAndApplyState } from '../stores/state'
import { locks, refreshAll, scope } from '../stores/status'
import { restartSuffix, showToast } from '../stores/toast'

const presets = [
  {
    id: 'fake-split',
    label: 'fake + multisplit',
    value: 'fake:blob=fake_default_tls:repeats=2 --lua-desync=multisplit:pos=1,midsld',
  },
  {
    id: 'hostfake',
    label: 'hostfakesplit',
    value: 'hostfakesplit:tcp_md5',
  },
  {
    id: 'disorder',
    label: 'multidisorder',
    value: 'multidisorder:pos=1,midsld',
  },
]

const profile = ref('1')
const desync = ref(presets[0].value)
const lockAfter = ref(true)
const lastStrategy = ref<number | null>(null)

const blobKind = ref<'tls' | 'wg'>('tls')
const blobName = ref('tls_custom_manual.bin')
const blobFile = ref<File | null>(null)
const selectAfter = ref(true)

const profileOptions = computed(() =>
  (locks.value || []).map((p) => ({
    id: String(p.profile),
    label: `${p.profile}. ${p.label}`,
    max: p.max_strategy,
  })))

const blobHint = computed(() =>
  blobKind.value === 'wg'
    ? 'Имя: wg_initial_fake_*.bin'
    : 'Имя: tls_*.bin или custom_tls.bin')

function applyPreset(id: string) {
  const p = presets.find((x) => x.id === id)
  if (p) desync.value = p.value
}

function onFile(ev: Event) {
  const input = ev.target as HTMLInputElement
  const f = input.files?.[0] || null
  blobFile.value = f
  if (f && !blobName.value) {
    blobName.value = f.name
  } else if (f && blobName.value === 'tls_custom_manual.bin' && blobKind.value === 'tls') {
    blobName.value = f.name.startsWith('tls_') || f.name === 'custom_tls.bin' ? f.name : `tls_${f.name.replace(/[^\w.-]+/g, '_')}`
  }
}

async function submitStrategy() {
  if (!desync.value.trim()) {
    showToast('Укажите строку desync', 'error')
    return
  }
  try {
    await withBusy('manual-strat', async () => {
      const res = await registerStrategy({
        profile: profile.value,
        desync: desync.value.trim(),
        lock: lockAfter.value,
        scope: scope.value,
      })
      lastStrategy.value = Number(res.strategy || 0) || null
      showToast(`Стратегия ${res.strategy} добавлена в профиль ${profile.value}.${restartSuffix(res)}`)
      await refreshAll()
      await fetchAndApplyState()
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}

async function submitBlob() {
  if (!blobFile.value) {
    showToast('Выберите .bin файл', 'error')
    return
  }
  const name = blobName.value.trim() || blobFile.value.name
  try {
    await withBusy('manual-blob', async () => {
      const res = await uploadBlob({
        kind: blobKind.value,
        name,
        file: blobFile.value!,
        select: selectAfter.value,
      })
      showToast(`Blob ${res.name} сохранён.${restartSuffix(res)}`)
      await refreshTlsBlobSettings()
      await refreshWgBlobSettings()
      if (selectAfter.value && blobKind.value === 'tls' && !res.selected) {
        await applySetting.tls_blob(name)
      }
    })
  } catch (e) {
    showToast((e as Error).message, 'error')
  }
}
</script>

<template>
  <section>
    <div class="page-head">
      <div>
        <h1>Добавить вручную</h1>
        <p>Тестовая лаборатория: свои стратегии desync и загрузка TLS/WG blob без правки сырого конфига.</p>
      </div>
    </div>

    <div class="lab-grid">
      <article class="panel strategy">
        <h2>Стратегия профиля</h2>
        <p class="panel-d">Строка добавляется в блок профиля как следующий номер после текущего max. Можно сразу зафиксировать.</p>

        <div class="grid-2" style="margin-top:0.85rem">
          <label class="field">
            <span>Профиль</span>
            <select v-model="profile" :disabled="busyActive">
              <option v-for="p in profileOptions" :key="p.id" :value="p.id">
                {{ p.label }} (max {{ p.max ?? '—' }})
              </option>
            </select>
          </label>
          <label class="field">
            <span>Шаблон</span>
            <select :disabled="busyActive" @change="applyPreset(($event.target as HTMLSelectElement).value)">
              <option disabled value="">выберите…</option>
              <option v-for="p in presets" :key="p.id" :value="p.id">{{ p.label }}</option>
            </select>
          </label>
        </div>

        <label class="field" style="margin-top:0.75rem">
          <span>Desync (без strategy=… — номер назначит сервер)</span>
          <textarea v-model="desync" rows="5" :disabled="busyActive"
            placeholder="fake:blob=fake_default_tls:repeats=2 --lua-desync=multisplit:pos=1,midsld"></textarea>
        </label>

        <label class="check-row" style="margin-top:0.65rem">
          <input v-model="lockAfter" type="checkbox" :disabled="busyActive">
          <span>Сразу зафиксировать новую стратегию</span>
        </label>

        <div class="actions" style="margin-top:0.85rem">
          <button type="button" class="primary" :disabled="busyActive"
            :class="{ 'is-busy': busyButton === 'manual-strat' }"
            @click="submitStrategy">Добавить стратегию</button>
        </div>
        <p v-if="lastStrategy != null" class="meta" style="margin-top:0.65rem">
          Последняя добавленная: #{{ lastStrategy }}
        </p>
      </article>

      <article class="panel strategy">
        <h2>Blob-файл</h2>
        <p class="panel-d">Файл попадает в <code>/opt/zator/files/fake</code> и появляется в списках TLS/WG.</p>

        <div class="grid-2" style="margin-top:0.85rem">
          <label class="field">
            <span>Тип</span>
            <select v-model="blobKind" :disabled="busyActive"
              @change="blobName = blobKind === 'wg' ? 'wg_initial_fake_manual.bin' : 'tls_custom_manual.bin'">
              <option value="tls">TLS</option>
              <option value="wg">WireGuard</option>
            </select>
          </label>
          <label class="field">
            <span>Имя на устройстве</span>
            <input v-model="blobName" :disabled="busyActive" :placeholder="blobHint">
          </label>
        </div>

        <label class="field" style="margin-top:0.75rem">
          <span>Файл .bin</span>
          <input type="file" accept=".bin,application/octet-stream" :disabled="busyActive" @change="onFile">
        </label>
        <p class="meta">{{ blobHint }}. Макс. 2 МБ.</p>

        <label class="check-row" style="margin-top:0.65rem">
          <input v-model="selectAfter" type="checkbox" :disabled="busyActive">
          <span>Сразу выбрать как текущий {{ blobKind === 'wg' ? 'WG' : 'TLS' }} blob</span>
        </label>

        <div class="actions" style="margin-top:0.85rem">
          <button type="button" class="primary" :disabled="busyActive || !blobFile"
            :class="{ 'is-busy': busyButton === 'manual-blob' }"
            @click="submitBlob">Загрузить blob</button>
        </div>

        <div class="meta" style="margin-top:0.85rem">
          TLS доступно: {{ (tlsBlobSettings?.available_blobs || []).length }} ·
          WG доступно: {{ (wgBlobSettings?.available_blobs || []).length }}
        </div>
      </article>
    </div>
  </section>
</template>
