import { Badge, Card } from '@roedu/ui';
import { SmartLink } from './SmartLink';
import type { EventRow } from '../screens/types';

export function EventCard({ e }: { e: EventRow }) {
  return (
    <Card>
      <h3 className="sa-acard__title">
        <a href={e.url}>{e.title}</a>
      </h3>
      {e.localPocMarker && <p><Badge tone="neutral" size="sm">{e.localPocMarker}</Badge>{e.pastLabel && <> <Badge tone="neutral" size="sm">{e.pastLabel}</Badge></>}</p>}
      {e.type && (
        <Badge tone="neutral" size="sm">
          {e.type}
        </Badge>
      )}
      <p className="sa-acard__meta">
        {e.when}
        {e.place && (
          <>
            {' · '}
            <SmartLink href={e.place.url}>{e.place.name}</SmartLink>
          </>
        )}
      </p>
      {e.description && <p>{e.description}</p>}
    </Card>
  );
}
