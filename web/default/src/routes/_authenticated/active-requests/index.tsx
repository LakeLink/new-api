import { createFileRoute, redirect } from '@tanstack/react-router'

import { ActiveRequestsPage } from '@/features/active-requests/active-requests-page'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

export const Route = createFileRoute('/_authenticated/active-requests/')({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()
    if (!auth.user || auth.user.role < ROLE.ADMIN) {
      throw redirect({ to: '/403' })
    }
  },
  component: ActiveRequestsPage,
})
