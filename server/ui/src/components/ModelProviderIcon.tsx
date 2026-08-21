import { Brain } from "lucide-react";
import {
	siAnthropic,
	siDeepseek,
	siGoogle,
	siMinimax,
	siMistralai,
	siMoonshotai,
	siOpenaigym,
	siQwen,
	siX,
	siZdotai,
} from "simple-icons";
import type { SimpleIcon } from "simple-icons";

const providerIcons: Record<string, SimpleIcon> = {
	anthropic: siAnthropic,
	deepseek: siDeepseek,
	google: siGoogle,
	minimax: siMinimax,
	mistralai: siMistralai,
	moonshotai: siMoonshotai,
	openai: siOpenaigym,
	qwen: siQwen,
	"x-ai": siX,
	"z-ai": siZdotai,
};

interface Props {
	namespace?: string;
	size?: number;
	className?: string;
}

export function ModelProviderIcon({ namespace, size = 12, className }: Props) {
	const icon = namespace ? providerIcons[namespace.toLowerCase()] : undefined;
	if (!icon)
		return <Brain size={size} className={className} aria-hidden="true" />;

	return (
		<svg
			viewBox="0 0 24 24"
			width={size}
			height={size}
			fill="currentColor"
			className={className}
			aria-hidden="true"
		>
			<path d={icon.path} />
		</svg>
	);
}
