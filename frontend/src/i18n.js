import { createI18n } from 'vue-i18n'

const localeLoaders = {
  en: () => import('./locales/en.json'),
  'zh-CN': () => import('./locales/zh-CN.json'),
  'zh-TW': () => import('./locales/zh-TW.json'),
  'zh-HK': () => import('./locales/zh-HK.json'),
  ru: () => import('./locales/ru.json')
}
const localeFlights = new Map()

function loadLocale(locale) {
  if (!localeFlights.has(locale)) {
    const flight = localeLoaders[locale]().catch((error) => {
      localeFlights.delete(locale)
      throw error
    })
    localeFlights.set(locale, flight)
  }
  return localeFlights.get(locale)
}

function detectSystemLocale() {
  const lang = (typeof navigator === 'undefined' ? 'en' : navigator.language).toLowerCase()
  if (lang.startsWith('zh')) {
    if (lang.includes('hk') || lang.includes('mo')) return 'zh-HK'
    if (lang.includes('tw') || lang === 'zh-hant') return 'zh-TW'
    return 'zh-CN'
  }
  if (lang.startsWith('ru')) return 'ru'
  return 'en'
}

export async function ensureLocaleMessages(composer, nextLocale) {
  if (!localeLoaders[nextLocale]) return false
  const available = Array.isArray(composer.availableLocales) ? composer.availableLocales : composer.availableLocales.value
  if (!available.includes(nextLocale)) {
    const module = await loadLocale(nextLocale)
    composer.setLocaleMessage(nextLocale, module.default)
  }
  return true
}

export async function createApplicationI18n() {
  const savedLocale = typeof localStorage === 'undefined' ? '' : localStorage.getItem('tunnelboard.locale')
  const locale = localeLoaders[savedLocale] ? savedLocale : detectSystemLocale()
  const [activeModule, fallbackModule] = await Promise.all([
    loadLocale(locale),
    locale === 'en' ? Promise.resolve(null) : loadLocale('en')
  ])
  const messages = { [locale]: activeModule.default }
  if (fallbackModule) messages.en = fallbackModule.default
  return createI18n({ legacy: false, locale, fallbackLocale: 'en', messages })
}
