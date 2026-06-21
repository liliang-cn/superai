import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import type { ComponentProps } from "react";

export type ActionsProps = ComponentProps<"div">;

export const Actions = ({ className, children, ...props }: ActionsProps) => (
  <div className={cn("flex items-center gap-1", className)} {...props}>
    {children}
  </div>
);

export type ActionProps = ComponentProps<typeof Button> & {
  tooltip?: string;
  label?: string;
};

export const Action = ({
  tooltip,
  label,
  children,
  className,
  variant = "ghost",
  size = "icon-sm",
  ...props
}: ActionProps) => (
  <Button
    className={cn(
      "relative text-muted-foreground hover:text-foreground",
      className,
    )}
    size={size}
    type="button"
    variant={variant}
    title={tooltip ?? label}
    aria-label={label ?? tooltip}
    {...props}
  >
    {children}
    <span className="sr-only">{label ?? tooltip}</span>
  </Button>
);
