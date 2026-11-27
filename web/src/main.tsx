import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import { ThemeProvider } from './theme'
import { FeedbackProvider } from './ui/Feedback'
import './styles.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ThemeProvider>
      <FeedbackProvider>
        <App />
      </FeedbackProvider>
    </ThemeProvider>
  </StrictMode>,
)
