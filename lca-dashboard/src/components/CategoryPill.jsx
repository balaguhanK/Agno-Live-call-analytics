// A call category as LCA shows it: a salmon pill, or a red one with a
// warning sign when the category is an alert. prefix adds LCA's
// "Category:" / "Alert:" wording, used for the line in the transcript.
export default function CategoryPill({ name, alert, prefix = false }) {
  const label = prefix ? `${alert ? 'Alert' : 'Category'}: ${name}` : name;
  return (
    <span className={`category-pill${alert ? ' alert' : ''}`} title={alert ? `${name} (alert)` : name}>
      {alert && <span aria-hidden="true">⚠ </span>}
      {label}
      {alert && !prefix && <span className="sr-only"> (alert)</span>}
    </span>
  );
}
