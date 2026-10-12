import { mount } from 'svelte'
import './app.css'
import { applyTheme } from './lib/theme'
import App from './App.svelte'

applyTheme()
mount(App, { target: document.getElementById('app')! })
