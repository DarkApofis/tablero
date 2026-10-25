import { Logo } from "@/components/logo";
import { AuthTabs } from "./auth-tabs";

export default function AuthLayout({ children }: LayoutProps<"/">) {
  return (
    <main className="flex flex-1 items-center justify-center px-4 py-12">
      <div className="flex w-full max-w-[360px] flex-col gap-[18px]">
        <Logo size="lg" />
        <AuthTabs />
        {children}
      </div>
    </main>
  );
}
