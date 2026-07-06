import type { AnchorHTMLAttributes, ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { isSpaPath } from '../screens/registry';

interface SmartLinkProps extends AnchorHTMLAttributes<HTMLAnchorElement> {
  href: string;
  children: ReactNode;
}

/**
 * Soft-navigates when the target is a migrated SPA route, plain full-page
 * navigation otherwise (Django still owns every non-migrated URL).
 */
export function SmartLink({ href, children, ...rest }: SmartLinkProps) {
  if (isSpaPath(href)) {
    return (
      <Link to={href} {...rest}>
        {children}
      </Link>
    );
  }
  return (
    <a href={href} {...rest}>
      {children}
    </a>
  );
}
