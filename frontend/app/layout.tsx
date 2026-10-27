import type { Metadata } from "next";
import { preconnect } from "react-dom";
import localFont from "next/font/local";
import { Providers } from "./providers";
import "./globals.css";

// Geist, like geist/font/sans but not preloaded: on a slow connection the preload
// competed with the JavaScript that fetches every screen's data. Text shows in the
// size-matched fallback until it arrives.
const geistSans = localFont({
  src: "../node_modules/geist/dist/fonts/geist-sans/Geist-Variable.woff2",
  variable: "--font-geist-sans",
  weight: "100 900",
  display: "swap",
  preload: false,
});

export const metadata: Metadata = {
  title: "Tablero",
  description: "Kanban colaborativo en tiempo real",
};

export default function RootLayout({ children }: LayoutProps<"/">) {
  // Every screen's data comes from the API, with credentials: open that connection
  // early. Not needed when the API is on this site, behind /api.
  const api = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";
  if (/^https?:\/\//.test(api)) preconnect(api, { crossOrigin: "use-credentials" });
  return (
    <html
      lang="es"
      className={`${geistSans.variable} h-full antialiased`}
    >
      <body className="min-h-full flex flex-col">
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
