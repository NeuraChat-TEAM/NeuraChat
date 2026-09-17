import React from "react"
import { createRoot } from "react-dom/client"
import App from "./app/App"
import { TooltipProvider } from "./shared/ui"
import "./shared/styles/globals.css"

createRoot(document.getElementById("root")!).render(
	<React.StrictMode>
		<TooltipProvider delayDuration={300}>
			<App />
		</TooltipProvider>
	</React.StrictMode>,
)
