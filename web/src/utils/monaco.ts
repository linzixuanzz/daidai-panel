import loader, { type Monaco } from '@monaco-editor/loader'

const MONACO_CDN_VS = 'https://cdn.jsdelivr.net/npm/monaco-editor@0.55.1/min/vs'
const LOCAL_MONACO_VS = 'monaco/vs'

type MonacoSource = 'local' | 'cdn'

export interface MonacoLoadResult {
  monaco: Monaco
  source: MonacoSource
}

let monacoPromise: Promise<MonacoLoadResult> | null = null

function getLocalMonacoWorkerUrl() {
  return `${import.meta.env.BASE_URL}${LOCAL_MONACO_VS}/loader.js`
}

async function canUseLocalMonaco() {
  const workerUrl = getLocalMonacoWorkerUrl()

  try {
    const headResponse = await fetch(workerUrl, { method: 'HEAD', cache: 'no-store' })
    if (headResponse.ok) {
      return true
    }
    if (headResponse.status !== 405) {
      return false
    }
  } catch {
    return false
  }

  try {
    const getResponse = await fetch(workerUrl, { method: 'GET', cache: 'no-store' })
    getResponse.body?.cancel?.()
    return getResponse.ok
  } catch {
    return false
  }
}

async function loadLocalMonaco(): Promise<MonacoLoadResult> {
  loader.config({
    paths: {
      vs: `${import.meta.env.BASE_URL}${LOCAL_MONACO_VS}`
    },
    'vs/nls': { availableLanguages: { '*': 'zh-cn' } }
  })

  const monaco = await loader.init()
  return { monaco, source: 'local' }
}

async function loadCdnMonaco(): Promise<MonacoLoadResult> {
  loader.config({
    paths: {
      vs: MONACO_CDN_VS
    },
    'vs/nls': { availableLanguages: { '*': 'zh-cn' } }
  })

  const monaco = await loader.init()
  return { monaco, source: 'cdn' }
}

export async function loadMonacoEditor(): Promise<MonacoLoadResult> {
  if (!monacoPromise) {
    monacoPromise = (async () => {
      if (await canUseLocalMonaco()) {
        try {
          return await loadLocalMonaco()
        } catch (error) {
          console.warn('本地 Monaco 资源加载失败，已回退到 CDN。', error)
        }
      }

      return loadCdnMonaco()
    })().catch((error) => {
      monacoPromise = null
      throw error
    })
  }

  return monacoPromise
}
