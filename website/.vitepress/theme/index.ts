import { h } from 'vue'
import DefaultTheme from 'vitepress/theme'
import './style.css'
import TerminalDemo from './components/TerminalDemo.vue'

export default {
  extends: DefaultTheme,
  Layout: () => {
    return h(DefaultTheme.Layout, null, {
      'home-hero-image': () => h(TerminalDemo),
    })
  },
}
