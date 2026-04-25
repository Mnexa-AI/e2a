"use client";

import { useAuth } from "../components/AuthProvider";
import { Sidebar } from "../components/Sidebar";
import { SignInLink } from "../components/SignInLink";

export default function AppLayout({ children }: { children: React.ReactNode }) {
  const { user, loading } = useAuth();

  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <p className="text-sm text-muted">Loading...</p>
      </div>
    );
  }

  if (!user) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <div className="text-center">
          <p className="text-muted mb-4">Sign in to access this page.</p>
          <SignInLink className="inline-block px-4 py-2 bg-accent text-white rounded-md text-sm font-medium hover:bg-accent-light transition">
            Sign in with Google
          </SignInLink>
        </div>
      </div>
    );
  }

  return (
    <div className="flex min-h-screen">
      <Sidebar />
      <main className="flex-1 overflow-auto">
        <div className="max-w-4xl mx-auto px-8 py-8">
          {children}
        </div>
      </main>
    </div>
  );
}
