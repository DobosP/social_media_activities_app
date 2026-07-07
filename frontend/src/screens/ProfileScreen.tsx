import { Badge, Button, Card, Stack } from '@roedu/ui';
import { SmartLink } from '../components/SmartLink';
import { TabStrip } from '../components/TabStrip';
import type { ScreenProps } from './registry';
import type { ProfileData } from './types3';

export function ProfileScreen({ payload }: ScreenProps) {
  const data = payload.data as ProfileData;
  const { ui } = data;
  const avatarSrc = data.avatarUrl || data.journeyAvatar;
  const currentPath = window.location.pathname;

  return (
    <div className="sa-screen">
      <h1>{data.name}</h1>
      <TabStrip tabs={data.tabs} />

      <Card>
        <Stack direction="row" gap="md" align="flex-start" wrap>
          {avatarSrc && <img className="avatar" src={avatarSrc} width={96} height={96} alt={data.name} />}
          <div className="u-flex-1">
            <p>
              <strong>{data.username}</strong>
            </p>
            {data.ageBand && <p className="muted">{data.ageBand}</p>}
            {data.identityVerified && (
              <Badge tone="success" size="sm">
                {ui.ageVerification}
              </Badge>
            )}
            <form method="post" action={data.actions.avatarUpload} encType="multipart/form-data" className="row">
              <input type="hidden" name="csrfmiddlewaretoken" value={payload.csrf} />
              <input type="file" name="image" accept="image/*" required aria-label={ui.updatePhoto} />
              <Button type="submit" size="sm">
                {ui.updatePhoto}
              </Button>
            </form>
          </div>
        </Stack>
      </Card>

      <Card>
        <div className="sa-section-head">
          <h2>{ui.connections}</h2>
          <SmartLink className="sa-more" href={data.actions.connections}>
            {ui.edit}
          </SmartLink>
        </div>
        {data.pendingIncomingCount > 0 && (
          <div className="banner">
            {ui.pendingRequests} · <SmartLink href={data.actions.connections}>{ui.review}</SmartLink>
          </div>
        )}
        {data.connections.length > 0 && (
          <ul className="members">
            {data.connections.map((connection) => (
              <li key={connection.publicId}>
                {connection.name}
                <form method="post" action={data.actions.connectionMessage} className="inline">
                  <input type="hidden" name="csrfmiddlewaretoken" value={payload.csrf} />
                  <input type="hidden" name="public_id" value={connection.publicId} />
                  <Button type="submit" size="sm">
                    {ui.message}
                  </Button>
                </form>
              </li>
            ))}
          </ul>
        )}
        {data.connectionsTotal > data.connections.length && (
          <p className="muted">
            <SmartLink href={data.actions.connections}>{ui.seeAllConnections}</SmartLink>
          </p>
        )}
      </Card>

      <Card>
        <h2>{ui.journey}</h2>
        <Stack direction="row" gap="md" align="center" wrap>
          {data.journeyAvatar && (
            <img className="avatar" src={data.journeyAvatar} width={120} height={120} alt={ui.journey} />
          )}
          {data.progression && (
            <Stack gap="xs">
              <strong>
                {data.progression.level} / {data.progression.maxLevel}
              </strong>
              <p className="muted">{data.progression.count}</p>
            </Stack>
          )}
        </Stack>
      </Card>

      <Card>
        <div className="sa-section-head">
          <h2>{ui.interests}</h2>
          <SmartLink className="sa-more" href={data.actions.interestsEdit}>
            {ui.edit}
          </SmartLink>
        </div>
        {data.interests.length > 0 && (
          <div className="tags">
            {data.interests.map((interest) => (
              <span key={interest} className="tag">
                {interest}
              </span>
            ))}
          </div>
        )}
      </Card>

      <details className="card" open={!data.provenance?.isCurrent || data.provenance.expiresSoon}>
        <summary>
          <h2 className="inline-h">{ui.ageVerification}</h2>
        </summary>
        {data.provenance ? (
          <Stack gap="sm">
            {data.provenance.bandDisplay && (
              <p>
                <strong>{ui.verifiedAs}</strong> {data.provenance.bandDisplay}
              </p>
            )}
            <p className="muted">
              {[data.provenance.method, data.provenance.provider, data.provenance.verifiedAt]
                .filter(Boolean)
                .join(' · ')}
            </p>
            <p className={data.provenance.expiresSoon ? 'banner' : 'muted'}>
              {[data.provenance.status === 'current' ? ui.current : data.provenance.status, data.provenance.expiresAt]
                .filter(Boolean)
                .join(' · ')}
            </p>
            <SmartLink className="btn" href={data.actions.verifyAge}>
              {ui.reVerify}
            </SmartLink>
          </Stack>
        ) : (
          <SmartLink className="btn" href={data.actions.verifyAge}>
            {ui.verifyEudi}
          </SmartLink>
        )}
      </details>

      {data.blocked.length > 0 && (
        <details className="card">
          <summary>
            <h2 className="inline-h">{ui.blocked}</h2> <span className="muted">({data.blocked.length})</span>
          </summary>
          <ul className="members">
            {data.blocked.map((blocked) => (
              <li key={blocked.pk}>
                {blocked.name}
                <form method="post" action={data.actions.unblock.replace('{pk}', String(blocked.pk))} className="inline">
                  <input type="hidden" name="csrfmiddlewaretoken" value={payload.csrf} />
                  <input type="hidden" name="next" value={currentPath} />
                  <Button type="submit" size="sm" variant="ghost">
                    {ui.unblock}
                  </Button>
                </form>
              </li>
            ))}
          </ul>
        </details>
      )}
    </div>
  );
}
