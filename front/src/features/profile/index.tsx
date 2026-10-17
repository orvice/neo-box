import { useAuth } from '@/hooks/use-auth'
import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Page, PageHeader, PageScroll } from '@/components/common/page-parts'

export function ProfilePage() {
  const { user } = useAuth()
  if (!user) return null
  const name = user.displayName || user.username

  return (
    <Page>
      <PageHeader
        className='max-w-3xl'
        title='Profile'
        subtitle='Your Logto account.'
      />
      <PageScroll className='max-w-3xl'>
        <Card>
          <CardHeader>
            <CardTitle>Account</CardTitle>
            <CardDescription>
              Your profile, password and sign-in methods are managed in Logto.
            </CardDescription>
          </CardHeader>
          <CardContent className='flex items-center gap-4'>
            <Avatar className='size-16'>
              <AvatarImage src={user.avatarUrl} alt={name} />
              <AvatarFallback>
                {name.trim().charAt(0).toUpperCase()}
              </AvatarFallback>
            </Avatar>
            <div>
              <p className='font-medium'>{name}</p>
              <p className='text-sm text-muted-foreground'>{user.username}</p>
            </div>
          </CardContent>
        </Card>
      </PageScroll>
    </Page>
  )
}
