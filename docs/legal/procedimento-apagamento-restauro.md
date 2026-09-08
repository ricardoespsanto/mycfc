# Procedimento de apagamento, fornecedores, objetos e restauro

Estado: política de backup/restauro aprovada em `2026-09-08`; implementação e prova pendentes no #111.

## Condições de início

Executar apenas pedido aprovado, identidade e relações resolvidas, matriz vigente associada à decisão e proteção do último administrador confirmada. Usar identificadores opacos nos registos operacionais.

## Execução idempotente

1. Bloquear e reler o caso; rejeitar estado ou versão inesperados.
2. Materializar plano assinado por categoria: apagar, anonimizar, restringir ou expirar.
3. Capturar, em filas protegidas, objetos, fornecedores e destinatários antes de limpar referências.
4. Tornar a conta indisponível, incrementar a versão credencial, revogar funções e sessões.
5. Revogar tokens/credenciais remotos e tentar desconexão oficial; remover sempre credenciais locais mesmo se o fornecedor estiver indisponível.
6. Apagar ou pseudonimizar dados relacionais em ordem determinística, incluindo valores pessoais em JSON/arrays.
7. Gravar ledger/tombstone mínimo, separado e cifrado, com referência pseudónima, âmbito, conclusão e validade superior à cópia restaurável mais antiga.
8. Confirmar a transação; executar trabalhos externos com repetição limitada e backoff.
9. Considerar objeto já ausente e fornecedor já desligado como sucesso idempotente.
10. Marcar concluído apenas sem trabalho obrigatório pendente/falhado. Falha fica recuperável e visível sem revelar chaves ou IDs externos.

## Objetos privados

A fila guarda a chave cifrada/protegida, tipo e estado; logs mostram apenas ID opaco e resultado. Apagar versões atuais e não correntes de foto de perfil, reparação/equipamento quando pessoal e futuros álbuns. Verificar ausência depois da operação. Uma restauração não pode recriar metadados para objeto tombstonado.

## Destinatários

Manter lista de notificações necessárias, fundamento de dispensa, tentativas e resposta, sem copiar o dado apagado. Se o destinatário for responsável autónomo (por exemplo, federação/autoridade), informar o titular do seu papel e canal quando o CFC não possa ordenar eliminação.

## Backups e restauro

Este procedimento descreve o controlo a implementar; **não é uma descrição de capacidade já existente**. Atualmente, o bucket versionado de backups não expira versões não correntes e o ensaio de restauro não reaplica tombstones. Do mesmo modo, uma operação comum de eliminação de fotografia pode deixar versões antigas no armazenamento versionado. Um caso não pode ser declarado tecnicamente concluído com base nestes passos até #111 fechar e testar essas lacunas.

- Backups permanecem cifrados, isolados do serviço e sujeitos ao calendário aprovado: diários por 30 dias e mensais por 365 dias; não prometer edição in-place. Versões não correntes e marcadores são eliminados até 24 horas após a janela aplicável.
- O ledger de tombstones fica cifrado fora do conjunto restaurado, ou com cópia independente, durante 24 meses.
- Backups Hetzner ficam limitados a sete cópias diárias rotativas. Um snapshot manual exige responsável, motivo e expiração não superior a 30 dias.
- Um restauro inicia em rede/ambiente isolado, com aplicação impedida de servir tráfego.
- Email, integrações externas e acesso de utilizadores permanecem bloqueados durante o restauro.
- Restaurar base, validar integridade, aplicar migrações, importar o ledger mais recente e reaplicar todos os apagamentos cujo dado possa existir no ponto restaurado.
- Verificar amostras e contagens por categoria, objetos e sessões; só então obter dupla aprovação operacional e abrir tráfego.
- Registar exercício sem nomes, emails, IP, dados médicos, chaves de objeto ou IDs de fornecedor.

## Alertas e evidência

Alertar trabalhos bloqueados, repetição esgotada, prazo legal em risco e incompatibilidade ledger/backup. Fazer exercício anual com utilizador sintético maximamente ligado e repetir após qualquer alteração material ao backup, restauro ou executor. Guardar resultado, versão da matriz, tempos, falhas e correções; nunca dados reais de atleta.
