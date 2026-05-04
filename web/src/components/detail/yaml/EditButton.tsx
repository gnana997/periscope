// EditButton — the small icon-only "edit YAML" button rendered by
// ResourceActions in the DetailPane action row. Click navigates the
// URL to the YAML tab in edit mode (?tab=yaml&edit=1); YamlView
// dispatches to the inline editor when the kind is in the registry
// and the user has patch permission.
//
// `disabled` greys out the button when the user lacks `patch` on the
// resource. Tooltip + sizing comes from <IconAction>.

import { useSearchParams } from "react-router-dom";
import { Pencil } from "lucide-react";
import { IconAction } from "../../IconAction";

interface EditButtonProps {
  disabled?: boolean;
  /** Tooltip body shown when disabled (RBAC reason). */
  disabledTooltip?: string | null;
}

export function EditButton({
  disabled = false,
  disabledTooltip,
}: EditButtonProps) {
  const [, setParams] = useSearchParams();

  const handleClick = () => {
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.set("tab", "yaml");
        next.set("edit", "1");
        return next;
      },
      { replace: true },
    );
  };

  return (
    <IconAction
      label="Edit YAML"
      icon={<Pencil size={14} />}
      onClick={handleClick}
      disabled={disabled}
      disabledTooltip={disabledTooltip}
    />
  );
}
