import type { Application } from '../api/applications';
import { Link } from '../router/Link';

export function ApplicationReference({
  id,
  applications,
}: {
  id: string | null;
  applications: Application[];
}) {
  if (id === null) return <>No application</>;
  const application = applications.find((entry) => entry.id === id);
  return application ? (
    <Link to="/applications">{application.name}</Link>
  ) : (
    <>Deleted application</>
  );
}
