import { useState } from 'react'
import { UserRound } from 'lucide-react'
import { toast } from 'sonner'
import { useChangePassword, useUpdateProfile, type AuthUser } from '@/api/auth'
import { useAuth } from '@/hooks/use-auth'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Page, PageHeader, PageScroll } from '@/components/common/page-parts'

function displayName(user: {
  display_name?: string
  displayName?: string
  username: string
}) {
  return user.display_name || user.displayName || user.username
}

function avatarUrl(user: { avatar_url?: string; avatarUrl?: string }) {
  return user.avatar_url || user.avatarUrl || ''
}

export function ProfilePage() {
  const { user, refreshUser } = useAuth()

  if (!user) return null

  return <ProfileContent key={user.id} user={user} refreshUser={refreshUser} />
}

function ProfileContent({
  user,
  refreshUser,
}: {
  user: AuthUser
  refreshUser: (user: AuthUser) => void
}) {
  const updateProfile = useUpdateProfile()
  const changePassword = useChangePassword()

  const [name, setName] = useState(() => displayName(user))
  const [avatar, setAvatar] = useState(() => avatarUrl(user))
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')

  function handleUpdateProfile() {
    const trimmed = name.trim()
    if (!trimmed) {
      toast.error('Display name is required')
      return
    }
    updateProfile.mutate(
      { display_name: trimmed, avatar_url: avatar.trim() },
      {
        onSuccess: (res) => {
          toast.success('Profile updated')
          if (res.user) refreshUser(res.user)
        },
        onError: (e) => toast.error(e.message),
      }
    )
  }

  function handleChangePassword() {
    if (!currentPassword) {
      toast.error('Current password is required')
      return
    }
    if (!newPassword) {
      toast.error('New password is required')
      return
    }
    if (newPassword !== confirmPassword) {
      toast.error('New passwords do not match')
      return
    }
    changePassword.mutate(
      { current_password: currentPassword, new_password: newPassword },
      {
        onSuccess: () => {
          toast.success('Password changed')
          setCurrentPassword('')
          setNewPassword('')
          setConfirmPassword('')
        },
        onError: (e) => toast.error(e.message),
      }
    )
  }

  return (
    <Page>
      <PageHeader
        className='max-w-3xl'
        title='Profile'
        subtitle='Manage your account details and password.'
      />
      <PageScroll className='max-w-3xl'>
        <div className='space-y-6'>
          <Card>
            <CardHeader>
              <CardTitle>Account</CardTitle>
              <CardDescription>
                Your identity and sign-in details.
              </CardDescription>
            </CardHeader>
            <CardContent className='space-y-4'>
              <div className='flex flex-wrap items-center gap-2 text-sm text-muted-foreground'>
                <span>@{user.username}</span>
                <Badge
                  variant={user.role === 'admin' ? 'default' : 'secondary'}
                >
                  {user.role || 'user'}
                </Badge>
              </div>
              <div className='flex items-center gap-4'>
                <div className='flex h-16 w-16 shrink-0 items-center justify-center overflow-hidden rounded-md border bg-muted'>
                  {avatar.trim() ? (
                    <img
                      src={avatar.trim()}
                      alt=''
                      className='h-full w-full object-cover outline outline-1 -outline-offset-1 outline-black/10 dark:outline-white/10'
                    />
                  ) : (
                    <UserRound className='h-7 w-7 text-muted-foreground' />
                  )}
                </div>
                <div className='min-w-0 flex-1 space-y-2'>
                  <Label htmlFor='avatar-url'>Avatar URL</Label>
                  <Input
                    id='avatar-url'
                    placeholder='https://…'
                    value={avatar}
                    onChange={(e) => setAvatar(e.target.value)}
                  />
                </div>
              </div>
              <div className='space-y-2'>
                <Label htmlFor='display-name'>Display name</Label>
                <Input
                  id='display-name'
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                />
              </div>
              <Button
                onClick={handleUpdateProfile}
                disabled={updateProfile.isPending}
              >
                {updateProfile.isPending ? 'Saving…' : 'Save profile'}
              </Button>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Password</CardTitle>
              <CardDescription>
                Change the password you use to sign in.
              </CardDescription>
            </CardHeader>
            <CardContent className='space-y-4'>
              <div className='space-y-2'>
                <Label htmlFor='current-password'>Current password</Label>
                <Input
                  id='current-password'
                  type='password'
                  value={currentPassword}
                  onChange={(e) => setCurrentPassword(e.target.value)}
                  autoComplete='current-password'
                />
              </div>
              <div className='space-y-2'>
                <Label htmlFor='new-password'>New password</Label>
                <Input
                  id='new-password'
                  type='password'
                  value={newPassword}
                  onChange={(e) => setNewPassword(e.target.value)}
                  autoComplete='new-password'
                />
              </div>
              <div className='space-y-2'>
                <Label htmlFor='confirm-password'>Confirm new password</Label>
                <Input
                  id='confirm-password'
                  type='password'
                  value={confirmPassword}
                  onChange={(e) => setConfirmPassword(e.target.value)}
                  autoComplete='new-password'
                />
              </div>
              <Button
                onClick={handleChangePassword}
                disabled={changePassword.isPending}
              >
                {changePassword.isPending ? 'Updating…' : 'Change password'}
              </Button>
            </CardContent>
          </Card>
        </div>
      </PageScroll>
    </Page>
  )
}
